package notify

import (
	"context"
	"errors"
	"testing"
)

type fakeSender struct {
	pushErr error
	smsErr  error
	pushes  int
	sms     int
}

func (f *fakeSender) SendPush(context.Context, string, string, string) error {
	f.pushes++
	return f.pushErr
}

func (f *fakeSender) SendSMS(context.Context, string, string) error {
	f.sms++
	return f.smsErr
}

func TestDeliverPush(t *testing.T) {
	s := &fakeSender{}
	channel, err := Deliver(context.Background(), s, "tok", "0551234987", "Title", "Body")
	if err != nil || channel != "push" || s.pushes != 1 || s.sms != 0 {
		t.Fatalf("channel=%s err=%v pushes=%d sms=%d", channel, err, s.pushes, s.sms)
	}
}

func TestDeliverFallsBackToSMS(t *testing.T) {
	s := &fakeSender{pushErr: errors.New("token rejected")}
	channel, err := Deliver(context.Background(), s, "tok", "0551234987", "Title", "Body")
	if err != nil || channel != "sms" || s.sms != 1 {
		t.Fatalf("channel=%s err=%v sms=%d", channel, err, s.sms)
	}
}

func TestDeliverSMSWhenNoToken(t *testing.T) {
	s := &fakeSender{}
	channel, err := Deliver(context.Background(), s, "", "0551234987", "Title", "Body")
	if err != nil || channel != "sms" || s.pushes != 0 {
		t.Fatalf("channel=%s err=%v pushes=%d", channel, err, s.pushes)
	}
}
