package auth

import (
	"strings"
	"testing"
)

func TestAdminPasswordHash(t *testing.T) {
	password := "synthetic-admin-password-42!"
	hash, err := HashAdminPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyAdminPassword(password, hash) || verifyAdminPassword("incorrect", hash) {
		t.Fatal("password hash verification failed")
	}
	other, err := HashAdminPassword(password)
	if err != nil || other == hash {
		t.Fatal("each password hash must have a fresh random salt")
	}
	for _, invalid := range []string{"", "plaintext", strings.Replace(hash, "m=19456", "m=99999999", 1), hash + "extra", strings.Replace(hash, "$v=19$", "$v=16$", 1)} {
		if validAdminPasswordHash(invalid) || verifyAdminPassword(password, invalid) {
			t.Fatal("invalid or unbounded password hash accepted")
		}
	}
	if _, err := HashAdminPassword("short"); err == nil {
		t.Fatal("short passwords must be rejected at setup")
	}
	if _, err := HashAdminPassword(strings.Repeat("a", 1025)); err == nil {
		t.Fatal("oversized passwords must be rejected")
	}
}

func TestPasswordAdminDoesNotRequireMail(t *testing.T) {
	hash, err := HashAdminPassword("synthetic-admin-password-42!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Secret: strings.Repeat("a1", 32), AdminUsername: "test-admin", AdminTOTP: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", AdminPasswordHash: hash}
	a, err := New(nil, cfg)
	if err != nil || !a.AdminConfigured() || !a.AdminPasswordLogin() || a.Ready() {
		t.Fatal("password and TOTP login must work without email configuration")
	}
	oldVersion := a.version("admin", cfg.AdminUsername, 0)
	cfg.AdminPasswordHash, _ = HashAdminPassword("rotated-synthetic-password-42!")
	rotated, err := New(nil, cfg)
	if err != nil || oldVersion == rotated.version("admin", cfg.AdminUsername, 0) {
		t.Fatal("changing password must invalidate previous sessions")
	}
	cfg.AdminPasswordHash = "invalid"
	if _, err = New(nil, cfg); err == nil {
		t.Fatal("malformed password configuration must fail closed")
	}
}
