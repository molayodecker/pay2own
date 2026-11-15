package notify

import "context"

// Hub uses whichever of push and SMS is configured.
type Hub struct {
	Push interface {
		SendPush(ctx context.Context, token, title, body string) error
	}
	Text interface {
		SendSMS(ctx context.Context, phone, body string) error
	}
}

func (h Hub) SendPush(ctx context.Context, token, title, body string) error {
	if h.Push == nil {
		return ErrNotConfigured
	}
	return h.Push.SendPush(ctx, token, title, body)
}

func (h Hub) SendSMS(ctx context.Context, phone, body string) error {
	if h.Text == nil {
		return ErrNotConfigured
	}
	return h.Text.SendSMS(ctx, phone, body)
}

// TwilioFromNumber prefers TWILIO_PHONE_NUMBER and falls back to TWILIO_FROM_NUMBER.
func TwilioFromNumber(phoneNumber, fromNumber string) string {
	if phoneNumber != "" {
		return phoneNumber
	}
	return fromNumber
}

// FromEnv builds a sender from FIREBASE_SERVICE_ACCOUNT_FILE and Twilio
// (TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, and TWILIO_MESSAGING_SERVICE_SID or a from-number).
// It returns nil when neither channel is configured.
func FromEnv(firebaseFile, accountSID, authToken, messagingServiceSID, fromNumber string) (Sender, error) {
	fcm, err := NewFCM(firebaseFile)
	if err != nil {
		return nil, err
	}
	sms := NewTwilio(accountSID, authToken, messagingServiceSID, fromNumber)
	if fcm == nil && sms == nil {
		return nil, nil
	}
	return Hub{Push: fcm, Text: sms}, nil
}
