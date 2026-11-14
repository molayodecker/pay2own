package paystack

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type Client struct {
	secretKey string
	baseURL   string
	http      *http.Client
}

func New(secretKey string) *Client {
	return &Client{
		secretKey: secretKey,
		baseURL:   "https://api.paystack.co",
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

type ChargeRequest struct {
	Reference   string
	Email       string
	AmountMinor int64
	Currency    string
	Phone       string // e.g. 0551234987
	Provider    string // "mtn", "vod" (Telecel) or "atl" (AT)
}

// Charge asks Paystack to send a mobile-money approval prompt to the customer's phone.
// It returns Paystack's status, e.g. "pay_offline" (waiting for PIN) or "send_otp".
func (c *Client) Charge(ctx context.Context, req ChargeRequest) (string, error) {
	body := map[string]any{
		"reference": req.Reference,
		"email":     req.Email,
		"amount":    strconv.FormatInt(req.AmountMinor, 10),
		"currency":  req.Currency,
		"mobile_money": map[string]string{
			"phone":    req.Phone,
			"provider": req.Provider,
		},
	}

	log.Printf("paystack charge request: %+v", body)

	var out struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Status string `json:"status"`
		} `json:"data"`
	}

	if err := c.do(ctx, http.MethodPost, "/charge", body, &out); err != nil {
		return "", err
	}


	if !out.Status {
		return "", fmt.Errorf("paystack charge refused: %s", out.Message)
	}
	return out.Data.Status, nil
}

type Verification struct {
	Status      string // "success", "failed", "abandoned", ...
	AmountMinor int64
	Currency    string
}

// Verify asks Paystack what really happened to a payment. Never trust a notification without this.
func (c *Client) Verify(ctx context.Context, reference string) (Verification, error) {
	var out struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Status   string `json:"status"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/transaction/verify/"+reference, nil, &out); err != nil {
		return Verification{}, err
	}
	if !out.Status {
		return Verification{}, fmt.Errorf("paystack verify refused: %s", out.Message)
	}
	return Verification{Status: out.Data.Status, AmountMinor: out.Data.Amount, Currency: out.Data.Currency}, nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("paystack %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("paystack %s %s: status %d", method, path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}