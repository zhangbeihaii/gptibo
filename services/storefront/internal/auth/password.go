package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"gptibo/storefront/internal/domains"
	"gptibo/storefront/internal/fault"
	"gptibo/storefront/internal/security"
)

// Fix the accepted parameters so a malformed configuration cannot request
// unbounded CPU or memory during login. The salt is unique for every hash.
const adminPasswordPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

func HashAdminPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("administrator password requires 12 to 1024 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return adminPasswordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

func decodeAdminPasswordHash(encoded string) (salt, key []byte, ok bool) {
	if len(encoded) > 150 || !strings.HasPrefix(encoded, adminPasswordPrefix) {
		return nil, nil, false
	}
	parts := strings.Split(strings.TrimPrefix(encoded, adminPasswordPrefix), "$")
	if len(parts) != 2 {
		return nil, nil, false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[0])
	if err != nil || len(salt) != 16 {
		return nil, nil, false
	}
	key, err = base64.RawStdEncoding.Strict().DecodeString(parts[1])
	return salt, key, err == nil && len(key) == 32
}

func validAdminPasswordHash(encoded string) bool {
	_, _, ok := decodeAdminPasswordHash(encoded)
	return ok
}

func verifyAdminPassword(password, encoded string) bool {
	salt, key, ok := decodeAdminPasswordHash(encoded)
	if !ok || len(password) == 0 || len(password) > 1024 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(actual, key) == 1
}

func (a *Service) AdminPasswordLogin() bool {
	return a != nil && a.cfg.AdminPasswordHash != ""
}

func (a *Service) verifyPasswordAdmin(ctx context.Context, in VerifyInput, ip string) (string, error) {
	if !a.AdminConfigured() {
		return "", fault.New(503, "后台登录尚未配置。")
	}
	// The account limit applies across IP addresses and before the expensive hash.
	if err := a.Limit(ctx, "admin:verify:ip:"+ip, 20, 900); err != nil {
		return "", err
	}
	if err := a.Limit(ctx, "admin:verify:account", 30, 900); err != nil {
		return "", err
	}
	// Bound memory even if many source IPs attempt to log in simultaneously.
	select {
	case a.adminPasswordGate <- struct{}{}:
	default:
		return "", fault.New(429, "操作过于频繁，请稍后重试。")
	}
	passwordOK := verifyAdminPassword(in.Password, a.cfg.AdminPasswordHash)
	<-a.adminPasswordGate
	if !passwordOK || !security.Equal(in.Username, a.cfg.AdminUsername) {
		return "", invalid()
	}
	step := security.VerifyTOTP(a.cfg.AdminTOTP, in.Code, time.Now())
	if step < 0 {
		return "", invalid()
	}
	credential := a.version("admin", a.cfg.AdminUsername, 0)
	tx, err := a.Store.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO admin_totp_usage(credential_version,last_step) VALUES($1,$2)
 ON CONFLICT(credential_version) DO UPDATE SET last_step=excluded.last_step WHERE admin_totp_usage.last_step<excluded.last_step`, credential, step)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() != 1 {
		return "", invalid()
	}
	token := security.Token()
	if _, err = tx.Exec(ctx, "INSERT INTO auth_sessions(token_hash,role,subject,credential_version,domain_scope) VALUES($1,'admin',$2,$3,$4)", security.Hash(token), a.cfg.AdminUsername, credential, domains.Current(ctx).AuthScope("admin")); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}
