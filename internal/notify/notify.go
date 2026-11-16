package notify

import (
	"context"
	"errors"
)

// ErrNotConfigured means that channel has no credentials.
var ErrNotConfigured = errors.New("notifications are not configured")

// Sender delivers a message by push, SMS, or both.
type Sender interface {
	SendPush(ctx context.Context, token, title, body string) error
	SendSMS(ctx context.Context, phone, body string) error
}

// Deliver tries a push first and falls back to SMS when the push cannot be sent.
// The returned channel is "push" or "sms".
func Deliver(ctx context.Context, sender Sender, token, phone, title, body string) (string, error) {
	var pushErr error
	if token != "" {
		pushErr = sender.SendPush(ctx, token, title, body)
		if pushErr == nil {
			return "push", nil
		}
	}
	if phone != "" {
		err := sender.SendSMS(ctx, phone, body)
		if err == nil {
			return "sms", nil
		}
		if !errors.Is(err, ErrNotConfigured) {
			return "", err
		}
	}
	if pushErr != nil {
		return "", pushErr
	}
	return "", ErrNotConfigured
}
