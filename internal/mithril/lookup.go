// Package mithril asks the Instaclean API whether a worker already has an account.
package mithril

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	localBase      = "http://127.0.0.1:4000"
	productionBase = "https://api.tryinstaclean.com"
	localToken     = "pay2own-local-dev"
)

// APIBase chooses the Mithril host. An explicit MITHRIL_API_URL wins.
// Production uses the public API. Every other environment uses the local server.
func APIBase(appEnv, override string) string {
	if base := strings.TrimRight(strings.TrimSpace(override), "/"); base != "" {
		return base
	}
	if strings.EqualFold(strings.TrimSpace(appEnv), "production") {
		return productionBase
	}
	return localBase
}

// IntakeToken is the shared lookup secret. Local development has a fixed token
// so the site can reach a local Mithril without extra setup. Production must set one.
func IntakeToken(appEnv, configured string) string {
	if token := strings.TrimSpace(configured); token != "" {
		return token
	}
	if strings.EqualFold(strings.TrimSpace(appEnv), "production") {
		return ""
	}
	return localToken
}
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// HasAccount reports whether Mithril has a cleaner account for this phone or worker id.
func (c Client) HasAccount(ctx context.Context, phone, workerID string) (bool, error) {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" || c.Token == "" {
		return false, fmt.Errorf("mithril lookup is not configured")
	}
	body, err := json.Marshal(map[string]string{"phone": phone, "workerId": workerID})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/pay2own/workers/lookup", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-pay2own-token", c.Token)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("mithril lookup status %d", resp.StatusCode)
	}
	var parsed struct {
		Account bool `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return false, err
	}
	return parsed.Account, nil
}
