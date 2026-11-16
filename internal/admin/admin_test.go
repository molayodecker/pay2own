package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pay2own.com/backend/internal/store"
)

func TestLoginAndSale(t *testing.T) {
	shop := &fakeShop{token: "abc123tokenvalue000000000000000000000000000000000000000000000000"}
	h := New(shop, "shop-secret", []byte("0123456789abcdef"), Provision{}, nil)

	res := get(t, h, "/admin", "")
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/login" {
		t.Fatalf("unauthenticated: %d %s", res.Code, res.Header().Get("Location"))
	}

	bad := post(t, h, "/admin/login", "", url.Values{"password": {"nope"}})
	if bad.Code != http.StatusUnauthorized || !strings.Contains(bad.Body.String(), "Wrong password") {
		t.Fatalf("bad password: %d %s", bad.Code, bad.Body.String())
	}

	good := post(t, h, "/admin/login", "", url.Values{"password": {"shop-secret"}})
	if good.Code != http.StatusSeeOther {
		t.Fatalf("login: %d", good.Code)
	}
	cookie := good.Result().Cookies()[0].Name + "=" + good.Result().Cookies()[0].Value

	home := get(t, h, "/admin", cookie)
	if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "New sale") || !strings.Contains(home.Body.String(), "Can pay a month") {
		t.Fatalf("home: %d", home.Code)
	}
	csrf := csrfFrom(t, home.Body.String())

	sale := post(t, h, "/admin/sales", cookie, url.Values{
		"csrf": {csrf}, "name": {"Ama Mensah"}, "phone": {"0551234987"}, "network": {"mtn"},
		"imei": {"000000000000003"}, "model": {"TECNO Spark 20"}, "price": {"1500"},
		"deposit": {"300"}, "weeks": {"4"}, "first-due": {"2026-12-01"},
	})
	body := sale.Body.String()
	if sale.Code != http.StatusOK || !strings.Contains(body, shop.token) || !strings.Contains(body, "pay2own://enroll") {
		t.Fatalf("sale page: %d token shown %v", sale.Code, strings.Contains(body, shop.token))
	}
	if strings.Contains(body, "PROVISIONING_APK_URL") == false {
		t.Fatal("expected the unconfigured device-owner note")
	}
	if !shop.created.Restrict || shop.created.PriceMinor != 150000 || shop.created.DepositMinor != 30000 {
		t.Fatalf("saved %+v", shop.created)
	}
}

func TestProvisionQRWhenConfigured(t *testing.T) {
	shop := &fakeShop{token: "abc123tokenvalue000000000000000000000000000000000000000000000000"}
	h := New(shop, "shop-secret", []byte("0123456789abcdef"), Provision{
		APKURL: "https://example.com/pay2own.apk", SignatureChecksum: "abc",
	}, nil)
	cookie, csrf := signIn(t, h)
	sale := post(t, h, "/admin/sales", cookie, url.Values{
		"csrf": {csrf}, "name": {"Ama Mensah"}, "phone": {"0551234987"}, "network": {"mtn"},
		"imei": {"000000000000003"}, "model": {"TECNO Spark 20"}, "price": {"1500"},
		"deposit": {"300"}, "weeks": {"4"}, "first-due": {"2026-12-01"},
	})
	if sale.Code != http.StatusOK || strings.Contains(sale.Body.String(), "PROVISIONING_APK_URL") {
		t.Fatalf("provisioned page: %d", sale.Code)
	}
	if !strings.Contains(sale.Body.String(), "data:image/png;base64,") {
		t.Fatal("missing qr image")
	}
}

func TestReleasedPhoneIsNotLockedAgain(t *testing.T) {
	shop := &fakeShop{
		activateErr: store.ErrNotActivatable,
		device:      store.AdminDevice{IMEI: "000000000000003", Model: "TECNO", RestrictionState: "released", CustomerName: "Ama"},
	}
	h := New(shop, "shop-secret", []byte("0123456789abcdef"), Provision{}, nil)
	cookie, csrf := signIn(t, h)
	res := post(t, h, "/admin/devices/000000000000003/activate", cookie, url.Values{"csrf": {csrf}})
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "not locked again") {
		t.Fatalf("activate: %d %s", res.Code, res.Body.String())
	}
}

func TestModelFillsFromIMEI(t *testing.T) {
	old := modelLookup
	modelLookup = func(_ context.Context, imei string) (string, error) {
		if imei != "351752105755418" {
			t.Errorf("imei %s", imei)
		}
		return "Samsung Galaxy Note 9", nil
	}
	t.Cleanup(func() { modelLookup = old })

	h := New(&fakeShop{}, "shop-secret", []byte("0123456789abcdef"), Provision{}, nil)
	if res := get(t, h, "/admin/model?imei=351752105755418", ""); res.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", res.Code)
	}
	cookie, _ := signIn(t, h)
	res := get(t, h, "/admin/model?imei=351752105755418", cookie)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"model":"Samsung Galaxy Note 9"`) {
		t.Fatalf("lookup: %d %s", res.Code, res.Body.String())
	}
	home := get(t, h, "/admin", cookie)
	if !strings.Contains(home.Body.String(), "/admin/model?imei=") {
		t.Fatal("sale form does not look up the model")
	}
}

func TestTACProbeHidesTheSerial(t *testing.T) {
	if got := tacProbe("351752105755418"); got != "351752100000000" {
		t.Fatalf("probe %s", got)
	}
	if got := deviceName("TECNO", "B6S"); got != "TECNO B6S" {
		t.Fatalf("factory code %s", got)
	}
	if got := deviceName("Samsung", "Samsung Galaxy Note 9"); got != "Samsung Galaxy Note 9" {
		t.Fatalf("branded name %s", got)
	}
}

func TestLookupDeviceModel(t *testing.T) {
	var probe string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe = r.URL.Query().Get("imei")
		_, _ = w.Write([]byte(`{"device":{"brand":"Samsung","model":"Samsung Galaxy Note 9"}}`))
	}))
	t.Cleanup(srv.Close)
	old := tacLookupBase
	tacLookupBase = srv.URL
	t.Cleanup(func() { tacLookupBase = old })

	name, err := lookupDeviceModel(context.Background(), "351752105755418")
	if err != nil || name != "Samsung Galaxy Note 9" || probe != "351752100000000" {
		t.Fatalf("name %q err %v probe %s", name, err, probe)
	}
}

func TestProvisionPayload(t *testing.T) {
	raw, ok := (Provision{APKURL: "https://example.com/a.apk", SignatureChecksum: "abc", WifiSSID: "Shop", WifiPassword: "pw"}).payload()
	if !ok || !strings.Contains(raw, "com.pay2own/com.pay2own.Pay2OwnDeviceAdminReceiver") || !strings.Contains(raw, `"Shop"`) || !strings.Contains(raw, "PROVISIONING_LOCAL_TIME") {
		t.Fatalf("payload %s ok %v", raw, ok)
	}
	if _, ok := (Provision{APKURL: "https://example.com/a.apk"}).payload(); ok {
		t.Fatal("checksum is required")
	}
}

func signIn(t *testing.T, h http.Handler) (cookie, csrf string) {
	t.Helper()
	good := post(t, h, "/admin/login", "", url.Values{"password": {"shop-secret"}})
	if good.Code != http.StatusSeeOther || len(good.Result().Cookies()) == 0 {
		t.Fatalf("login: %d", good.Code)
	}
	c := good.Result().Cookies()[0]
	cookie = c.Name + "=" + c.Value
	home := get(t, h, "/admin", cookie)
	return cookie, csrfFrom(t, home.Body.String())
}

func csrfFrom(t *testing.T, body string) string {
	t.Helper()
	const marker = `name="csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("csrf missing")
	}
	rest := body[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

func get(t *testing.T, h http.Handler, path, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func post(t *testing.T, h http.Handler, path, cookie string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type fakeShop struct {
	token       string
	created     store.Sale
	device      store.AdminDevice
	activateErr error
}

func (f *fakeShop) CreateSale(_ context.Context, sale store.Sale) (int64, error) {
	f.created = sale
	return 9, nil
}
func (f *fakeShop) ActivateDevice(context.Context, string) error { return f.activateErr }
func (f *fakeShop) IssueDeviceToken(context.Context, string) (string, error) {
	if f.token == "" {
		return "", errors.New("no token")
	}
	return f.token, nil
}
func (f *fakeShop) DeviceForAdmin(context.Context, string) (store.AdminDevice, error) {
	if f.device.IMEI == "" {
		return store.AdminDevice{}, store.ErrNotFound
	}
	return f.device, nil
}

func TestAdminLoginThrottlesRepeatedFailures(t *testing.T) {
	h := New(&fakeShop{}, "shop-secret", []byte("0123456789abcdef"), Provision{}, nil)
	for i := 0; i < 5; i++ {
		res := post(t, h, "/admin/login", "", url.Values{"password": {"wrong"}})
		if res.Code != http.StatusUnauthorized { t.Fatalf("attempt %d: %d", i, res.Code) }
	}
	blocked := post(t, h, "/admin/login", "", url.Values{"password": {"shop-secret"}})
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("expected throttled sign-in, got %d", blocked.Code)
	}
}

func TestAdminCookieRequiresHTTPS(t *testing.T) {
	h := New(&fakeShop{}, "shop-secret", []byte("0123456789abcdef"), Provision{}, nil)
	resp := post(t, h, "/admin/login", "", url.Values{"password": {"shop-secret"}})
	if resp.Code != http.StatusSeeOther { t.Fatalf("login: %d", resp.Code) }
	if len(resp.Result().Cookies()) == 0 || !resp.Result().Cookies()[0].Secure {
		t.Fatal("session cookie must be Secure behind Fly TLS termination")
	}
}
