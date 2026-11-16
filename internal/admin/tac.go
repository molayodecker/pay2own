package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	modelLookup   = lookupDeviceModel
	tacClient     = &http.Client{Timeout: 4 * time.Second}
	tacLookupBase = "https://imeilens.com/api/v1/imei/validate"
)

// lookupDeviceModel returns the marketing name for an IMEI, or "" when the
// type code is unknown. The request uses a stand-in serial so the handset's
// own serial number is not sent.
func lookupDeviceModel(ctx context.Context, imei string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tacLookupBase+"?imei="+tacProbe(imei), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Pay2Own")
	req.Header.Set("Accept", "application/json")
	res, err := tacClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("model lookup status %d", res.StatusCode)
	}
	var parsed struct {
		Device *struct {
			Brand string `json:"brand"`
			Model string `json:"model"`
		} `json:"device"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.Device == nil {
		return "", nil
	}
	return deviceName(parsed.Device.Brand, parsed.Device.Model), nil
}

func deviceName(brand, model string) string {
	brand = strings.TrimSpace(brand)
	model = strings.TrimSpace(model)
	if model == "" {
		return brand
	}
	if brand == "" || strings.HasPrefix(strings.ToLower(model), strings.ToLower(brand)) {
		return model
	}
	return brand + " " + model
}

// tacProbe keeps the type allocation code (the first 8 digits) and replaces
// the serial with zeros, then fixes the Luhn check digit.
func tacProbe(imei string) string {
	body := imei[:8] + "000000"
	return body + strconv.Itoa(luhnCheck(body))
}

func luhnCheck(body string) int {
	sum := 0
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if (len(body)-1-i)%2 == 0 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return (10 - (sum % 10)) % 10
}
