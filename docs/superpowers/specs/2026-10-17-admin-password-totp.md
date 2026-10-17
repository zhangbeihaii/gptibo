# Admin password and authenticator login

User-approved scope: configure the supplied administrator identity with a password and a phone authenticator, without requiring an outbound email provider. Build locally on Windows and deploy built artifacts to the Hong Kong GPTibo service.

- Add optional Go configuration `ADMIN_PASSWORD_HASH[_FILE]`; presence selects password plus TOTP login. Preserve email login when no password hash is configured for compatibility with existing installations and the separate Cloudflare backend.
- Use Argon2id with a random 16-byte salt, 19 MiB memory, two iterations, one lane, and a 32-byte key. Reject malformed encodings and unsupported parameters before hashing.
- Require username, password, and a valid TOTP in one login request. Preserve request origin checks, optional Turnstile, IP/account limits, secure cookies, transactional TOTP replay protection, and session revocation on credential changes. Email challenges cannot bypass password mode.
- Display the appropriate login form using the server session's login method. Avoid setup commands or server internals in product-facing setup messages.
- Prepare credentials locally through standard input; write only the hash and random authenticator secret into restricted server files. Keep the binding URI/QR local and private, outside the public site and Git.
- Verify hash/configuration unit tests, login integration behavior, frontend production build, Linux cross-compilation, and deployed login/session/logout behavior. Back up the binary, static site, and runtime configuration before deployment. No business-data migration is required.
