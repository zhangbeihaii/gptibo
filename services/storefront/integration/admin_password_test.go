package integration_test

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"gptibo/storefront/internal/auth"
	"gptibo/storefront/internal/httpapi"
	"gptibo/storefront/internal/security"
)

func TestAdminPasswordAndTOTP(t *testing.T) {
	_, app := fixture(t, true)
	const password = "synthetic-admin-password-42!"
	hash, err := auth.HashAdminPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	cfg := auth.Config{Secret: strings.Repeat("a1", 32), AdminUsername: "fixture-admin", AdminTOTP: testTOTP, AdminPasswordHash: hash}
	a, err := auth.New(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	router := func(a *auth.Service) http.Handler {
		return httpapi.Router(app, "fixture", slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Options{Origin: testOrigin, Auth: a})
	}
	h := router(a)
	session := object(t, request(h, "GET", "/api/admin/session", nil, nil), 200)
	if session["loginMethod"] != "password" || session["configured"] != true || session["mailReady"] != false {
		t.Fatal("password login must not depend on mail")
	}
	object(t, request(h, "GET", "/api/admin/plans", nil, nil), 401)
	object(t, request(h, "POST", "/api/admin/login/email-code", map[string]string{"username": cfg.AdminUsername}, nil), 409)
	for _, input := range []map[string]string{
		{"username": cfg.AdminUsername, "password": "wrong", "code": security.TOTP(testTOTP, time.Now().Unix()/30)},
		{"username": "unknown", "password": password, "code": security.TOTP(testTOTP, time.Now().Unix()/30)},
		{"username": cfg.AdminUsername, "password": password, "code": "invalid"},
		{"username": cfg.AdminUsername, "code": security.TOTP(testTOTP, time.Now().Unix()/30)},
	} {
		w := request(h, "POST", "/api/admin/login", input, nil)
		object(t, w, 401)
		if w.Header().Get("Set-Cookie") != "" {
			t.Fatal("failed login created a session")
		}
	}
	input := map[string]string{"username": cfg.AdminUsername, "password": password, "code": security.TOTP(testTOTP, time.Now().Unix()/30)}
	w := request(h, "POST", "/api/admin/login", input, nil)
	object(t, w, 200)
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-sf_admin" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("admin session must use a secure, HttpOnly, same-site cookie")
	}
	if object(t, request(h, "GET", "/api/admin/session", nil, cookie), 200)["authenticated"] != true {
		t.Fatal("valid password and TOTP did not authenticate")
	}
	object(t, request(h, "POST", "/api/admin/login", input, nil), 401)
	cfg.AdminPasswordHash, _ = auth.HashAdminPassword("rotated-synthetic-password-42!")
	rotated, err := auth.New(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if object(t, request(router(rotated), "GET", "/api/admin/session", nil, cookie), 200)["authenticated"] != false {
		t.Fatal("old session survived password rotation")
	}
	object(t, request(h, "POST", "/api/admin/logout", map[string]any{}, cookie), 200)
	object(t, request(h, "GET", "/api/admin/plans", nil, cookie), 401)
	// All requests, including failures with malformed credentials, count toward
	// the same account limit rather than permitting password guessing by IP rotation.
	for i := 0; i < 30; i++ {
		request(h, "POST", "/api/admin/login", map[string]string{"password": "wrong"}, nil)
	}
	object(t, request(h, "POST", "/api/admin/login", input, nil), 429)
}
