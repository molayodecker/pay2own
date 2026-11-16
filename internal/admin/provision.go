package admin

import (
	"encoding/json"
	"time"
)

// Provision is the release app a factory-reset phone downloads from the QR code.
// SignatureChecksum is the URL-safe base64 SHA-256 of the APK signing certificate.
type Provision struct {
	APKURL            string
	SignatureChecksum string
	PackageChecksum   string
	WifiSSID          string
	WifiPassword      string
	WifiSecurity      string
	ComponentName     string
}

func (p Provision) ready() bool {
	return p.APKURL != "" && p.SignatureChecksum != ""
}

func (p Provision) component() string {
	if p.ComponentName != "" {
		return p.ComponentName
	}
	return "com.pay2own/com.pay2own.Pay2OwnDeviceAdminReceiver"
}

// payload is the Android setup-wizard QR document.
// ok is false until an APK URL and signing-certificate checksum are configured.
func (p Provision) payload() (string, bool) {
	if !p.ready() {
		return "", false
	}
	body := map[string]any{
		"android.app.extra.PROVISIONING_DEVICE_ADMIN_COMPONENT_NAME":            p.component(),
		"android.app.extra.PROVISIONING_DEVICE_ADMIN_PACKAGE_DOWNLOAD_LOCATION": p.APKURL,
		"android.app.extra.PROVISIONING_DEVICE_ADMIN_SIGNATURE_CHECKSUM":        p.SignatureChecksum,
		"android.app.extra.PROVISIONING_LEAVE_ALL_SYSTEM_APPS_ENABLED":          true,
		// Factory reset leaves the clock on an old date. The download certificate
		// was issued today, so HTTPS fails on every network until the time is set.
		// Android applies this before it downloads the app.
		"android.app.extra.PROVISIONING_LOCAL_TIME": time.Now().UnixMilli(),
	}
	if p.PackageChecksum != "" {
		body["android.app.extra.PROVISIONING_DEVICE_ADMIN_PACKAGE_CHECKSUM"] = p.PackageChecksum
	}
	if p.WifiSSID != "" {
		body["android.app.extra.PROVISIONING_WIFI_SSID"] = p.WifiSSID
		body["android.app.extra.PROVISIONING_WIFI_PASSWORD"] = p.WifiPassword
		security := p.WifiSecurity
		if security == "" {
			security = "WPA"
		}
		body["android.app.extra.PROVISIONING_WIFI_SECURITY_TYPE"] = security
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", false
	}
	return string(raw), true
}
