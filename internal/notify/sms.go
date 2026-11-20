package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// SMS sends a text through Twilio's Messages API.
// A messaging service is used when one is set; otherwise the from-number is.
type SMS struct {
	accountSID       string
	authToken        string
	messagingService string
	from             string
	WhatsAppFrom     string
	ContentSID       string
	messagesURL      string
	client           *http.Client
}

// NewTwilio returns nil when the account, token, or sender is missing, which turns SMS off.
func NewTwilio(accountSID, authToken, messagingServiceSID, fromNumber string) *SMS {
	if accountSID == "" || authToken == "" || (messagingServiceSID == "" && fromNumber == "") {
		return nil
	}
	return &SMS{
		accountSID:       accountSID,
		authToken:        authToken,
		messagingService: messagingServiceSID,
		from:             fromNumber,
		client:           &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *SMS) SendSMS(ctx context.Context, phone, body string) error {
	if s == nil || s.accountSID == "" || s.authToken == "" {
		return ErrNotConfigured
	}
	to, err := e164(phone)
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("To", to)
	form.Set("Body", body)
	switch {
	case s.messagingService != "":
		form.Set("MessagingServiceSid", s.messagingService)
	case s.from != "":
		form.Set("From", s.from)
	default:
		return ErrNotConfigured
	}

	endpoint := s.messagesURL
	if endpoint == "" {
		endpoint = "https://api.twilio.com/2010-04-01/Accounts/" + url.PathEscape(s.accountSID) + "/Messages.json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.accountSID, s.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sms status %d", resp.StatusCode)
	}
	return nil
}

func (s *SMS) SendPush(context.Context, string, string, string) error { return ErrNotConfigured }

// DeliverText tries WhatsApp first and falls back to SMS when WhatsApp cannot send.
// The channel is "whatsapp", "sms", or "failed". A failed send does not return an error
// so the caller can still keep the order.
func (s *SMS) DeliverText(ctx context.Context, phone, text string, vars map[string]string) (string, error) {
	if s == nil {
		return "failed", nil
	}
	if s != nil && s.ContentSID != "" {
		if err := s.sendWhatsApp(ctx, phone, "", vars); err == nil {
			return "whatsapp", nil
		}
	} else if s != nil && s.WhatsAppFrom != "" {
		if err := s.sendWhatsApp(ctx, phone, text, nil); err == nil {
			return "whatsapp", nil
		}
	}
	if err := s.SendSMS(ctx, phone, text); err == nil {
		return "sms", nil
	}
	return "failed", nil
}

func (s *SMS) sendWhatsApp(ctx context.Context, phone, text string, vars map[string]string) error {
	if s == nil || s.accountSID == "" || s.authToken == "" {
		return ErrNotConfigured
	}
	to, err := e164(phone)
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("To", "whatsapp:"+to)
	switch {
	case s.ContentSID != "" && len(vars) > 0:
		encoded, err := json.Marshal(vars)
		if err != nil {
			return err
		}
		form.Set("ContentSid", s.ContentSID)
		form.Set("ContentVariables", string(encoded))
	case text != "":
		form.Set("Body", text)
	default:
		return ErrNotConfigured
	}
	switch {
	case s.messagingService != "":
		form.Set("MessagingServiceSid", s.messagingService)
	case s.WhatsAppFrom != "":
		from, err := e164(strings.TrimPrefix(s.WhatsAppFrom, "whatsapp:"))
		if err != nil {
			return err
		}
		form.Set("From", "whatsapp:"+from)
	case s.from != "":
		from, err := e164(s.from)
		if err != nil {
			return err
		}
		form.Set("From", "whatsapp:"+from)
	default:
		return ErrNotConfigured
	}
	endpoint := s.messagesURL
	if endpoint == "" {
		endpoint = "https://api.twilio.com/2010-04-01/Accounts/" + url.PathEscape(s.accountSID) + "/Messages.json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.accountSID, s.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("whatsapp status %d", resp.StatusCode)
	}
	return nil
}

var (
	e164Pattern = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
	localGH     = regexp.MustCompile(`^0\d{9}$`)
	ghanaCC     = regexp.MustCompile(`^233\d{9}$`)
	bareDigits  = regexp.MustCompile(`^\d{8,15}$`)
)

// e164 turns a stored MoMo number into the form Twilio requires.
// 0551234987 and 233551234987 both become +233551234987.
func e164(phone string) (string, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(phone) {
		switch {
		case r == '+' || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
			continue
		default:
			return "", fmt.Errorf("invalid phone %q", phone)
		}
	}
	digits := b.String()
	switch {
	case e164Pattern.MatchString(digits):
		return digits, nil
	case localGH.MatchString(digits):
		return "+233" + digits[1:], nil
	case ghanaCC.MatchString(digits), bareDigits.MatchString(digits):
		return "+" + digits, nil
	default:
		return "", fmt.Errorf("invalid phone %q", phone)
	}
}
