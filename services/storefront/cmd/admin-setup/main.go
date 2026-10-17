// admin-setup prepares administrator secrets locally without printing them.
package main

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gptibo/storefront/internal/auth"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Administrator setup failed; check input and output directory. Existing files are never overwritten.")
		os.Exit(1)
	}
	fmt.Println("Administrator credential files prepared.")
}

func run() error {
	username := flag.String("username", "", "administrator username")
	dir := flag.String("output", "", "new private directory for generated secrets")
	flag.Parse()
	if *dir == "" || *username == "" || len(*username) > 80 || strings.TrimSpace(*username) != *username {
		return errors.New("output and username required")
	}
	password, err := io.ReadAll(io.LimitReader(os.Stdin, 1026))
	if err != nil {
		return err
	}
	defer clear(password)
	hash, err := auth.HashAdminPassword(strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r"))
	if err != nil {
		return err
	}
	random := make([]byte, 20)
	if _, err = rand.Read(random); err != nil {
		return err
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random)
	uri := url.URL{Scheme: "otpauth", Host: "totp", Path: "/GPTibo:" + *username}
	query := url.Values{"secret": {secret}, "issuer": {"GPTibo"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	uri.RawQuery = query.Encode()
	if err = os.Mkdir(*dir, 0700); err != nil {
		return err
	}
	for name, value := range map[string]string{"password-hash": hash, "totp": secret, "authenticator-uri.txt": uri.String()} {
		f, err := os.OpenFile(filepath.Join(*dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(f, value+"\n")
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
