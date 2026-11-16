package notify

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// FCM sends notification messages through Firebase Cloud Messaging HTTP v1.
type FCM struct {
	projectID string
	email     string
	key       *rsa.PrivateKey
	client    *http.Client

	mu      sync.Mutex
	access  string
	expires time.Time
}

type serviceAccount struct {
	ProjectID  string `json:"project_id"`
	Email      string `json:"client_email"`
	PrivateKey string `json:"private_key"`
}

// NewFCM reads a Firebase service account file. When the path is empty, it
// uses FIREBASE_SERVICE_ACCOUNT (the JSON itself) so a host can pass the key
// without a file. With neither set, push is disabled.
func NewFCM(path string) (*FCM, error) {
	var raw []byte
	var err error
	switch {
	case path != "":
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	case os.Getenv("FIREBASE_SERVICE_ACCOUNT") != "":
		raw = []byte(os.Getenv("FIREBASE_SERVICE_ACCOUNT"))
	default:
		return nil, nil
	}
	var sa serviceAccount
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("firebase private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("firebase private key is not RSA")
	}
	if sa.ProjectID == "" || sa.Email == "" {
		return nil, fmt.Errorf("firebase service account is missing project_id or client_email")
	}
	return &FCM{
		projectID: sa.ProjectID,
		email:     sa.Email,
		key:       key,
		client:    &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (f *FCM) SendPush(ctx context.Context, token, title, body string) error {
	if f == nil {
		return ErrNotConfigured
	}
	access, err := f.token(ctx)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"message": map[string]any{
			"token": token,
			"notification": map[string]string{
				"title": title,
				"body":  body,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := "https://fcm.googleapis.com/v1/projects/" + url.PathEscape(f.projectID) + "/messages:send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("fcm status %d", resp.StatusCode)
	}
	return nil
}

func (f *FCM) SendSMS(context.Context, string, string) error { return ErrNotConfigured }

func (f *FCM) token(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access != "" && time.Now().Before(f.expires) {
		return f.access, nil
	}
	assertion, err := f.assertion()
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 || out.AccessToken == "" {
		return "", fmt.Errorf("firebase auth status %d", resp.StatusCode)
	}
	f.access = out.AccessToken
	ttl := out.ExpiresIn
	if ttl < 120 {
		ttl = 120
	}
	f.expires = time.Now().Add(time.Duration(ttl-60) * time.Second)
	return f.access, nil
}

func (f *FCM) assertion() (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	now := time.Now().Unix()
	claims, err := json.Marshal(map[string]any{
		"iss":   f.email,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now,
		"exp":   now + 3600,
	})
	if err != nil {
		return "", err
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
