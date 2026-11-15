package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookie = "pay2own_admin"
	sessionLife   = 12 * time.Hour
)

func sameSecret(got, want string) bool {
	sumGot := sha256.Sum256([]byte(got))
	sumWant := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(sumGot[:], sumWant[:]) == 1
}

func (h *Handler) configured() bool {
	return h.password != "" && len(h.secret) >= 16
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	csrf := hex.EncodeToString(buf)
	exp := time.Now().Add(sessionLife).Unix()
	raw := strconv.FormatInt(exp, 10) + "." + csrf
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(raw))
	value := raw + "." + hex.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/admin",
		Expires:  time.Unix(exp, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
	return csrf, nil
}

func (h *Handler) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) session(r *http.Request) (csrf string, ok bool) {
	if !h.configured() {
		return "", false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return "", false
	}
	raw := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(raw))
	want := hex.EncodeToString(mac.Sum(nil))
	gotMac, err := hex.DecodeString(parts[2])
	wantMac, err2 := hex.DecodeString(want)
	if err != nil || err2 != nil || subtle.ConstantTimeCompare(gotMac, wantMac) != 1 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return parts[1], true
}

func (h *Handler) checkCSRF(r *http.Request) bool {
	csrf, ok := h.session(r)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(csrf)) == 1
}
