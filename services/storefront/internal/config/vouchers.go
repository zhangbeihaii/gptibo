package config

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
)

type VoucherKeys struct {
	Encryption  []byte
	Fingerprint []byte
}

func LoadVoucherKeys() (*VoucherKeys, error) {
	encryptionFile := os.Getenv("VOUCHER_ENCRYPTION_KEY_FILE")
	fingerprintFile := os.Getenv("VOUCHER_FINGERPRINT_KEY_FILE")
	if encryptionFile == "" && fingerprintFile == "" && os.Getenv("VOUCHER_ENCRYPTION_KEY") == "" && os.Getenv("VOUCHER_FINGERPRINT_KEY") == "" {
		return nil, nil
	}
	if encryptionFile == "" || fingerprintFile == "" || os.Getenv("VOUCHER_ENCRYPTION_KEY") != "" || os.Getenv("VOUCHER_FINGERPRINT_KEY") != "" {
		return nil, errors.New("both voucher key files are required")
	}
	encryption, err := Secret("VOUCHER_ENCRYPTION_KEY")
	if err != nil {
		return nil, err
	}
	fingerprint, err := Secret("VOUCHER_FINGERPRINT_KEY")
	if err != nil {
		return nil, err
	}
	decode := func(name, value string) ([]byte, error) {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
		if err != nil {
			return nil, errors.New("invalid " + name + " encoding")
		}
		return decoded, nil
	}
	encryptionBytes, err := decode("voucher encryption key", encryption)
	if err != nil || len(encryptionBytes) != 32 {
		return nil, errors.New("voucher encryption key must be base64-encoded 32-byte data")
	}
	fingerprintBytes, err := decode("voucher fingerprint key", fingerprint)
	if err != nil || len(fingerprintBytes) < 32 {
		return nil, errors.New("voucher fingerprint key must be base64-encoded data of at least 32 bytes")
	}
	return &VoucherKeys{Encryption: encryptionBytes, Fingerprint: fingerprintBytes}, nil
}
