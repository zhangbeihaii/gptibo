# Self-Stocked Voucher Inventory Implementation Plan

> **For agentic workers:** Execute inline in this session, one task at a time. Keep the Go/PostgreSQL implementation aligned with the approved design spec.

**Goal:** Add encrypted, self-owned voucher inventory to the existing GPTibo Go storefront so paid orders receive one reserved code in their authorized order detail.

**Architecture:** Add a PostgreSQL inventory ledger and a Go service that encrypts codes with AES-256-GCM and deduplicates them with keyed HMAC fingerprints. Connect reservation to the existing order transaction, issuance/release to verified payment results, an admin-only stock API and page, and customer-only order detail display. The frontend remains shared; this implementation targets the deployed Go/PostgreSQL backend and does not add the separate Cloudflare D1 backend.

**Tech Stack:** Go 1.26, Gin, pgx, PostgreSQL migrations, React 19, TypeScript, Vite, existing admin/customer sessions.

## Global Constraints

- Work in the local Windows workspace; do not build or install dependencies on the server.
- Store no plaintext voucher codes in the database, logs, audit metadata, admin stock listings, or exports.
- Require independent 32-byte AES and HMAC secrets from runtime secret files; fail closed if they are missing or malformed.
- Accept at most 50 non-empty codes per import; validate the entire batch before writing any row.
- Reserve and issue inventory transactionally; repeated callbacks and page reads must return the same allocation.
- Never expose a code until its order is verified paid and not held, blocked, or refunded.
- Do not add supplier purchase credentials, supplier APIs, new payment channels, or send codes in email.
- Do not modify or stage `services/storefront/.release/`.

---

## File Structure

- `services/storefront/migrations/034_voucher_inventory.sql`: inventory and batch ledger, uniqueness constraints, and state checks.
- `services/storefront/internal/vouchers/service.go`: key parsing, batch validation, AES-GCM encryption/decryption, fingerprinting, stock summary, reserve/issue/release operations, and safe customer delivery.
- `services/storefront/internal/config/vouchers.go`: load and validate independent secret-file configuration.
- `services/storefront/internal/store/store.go`: grant the runtime DB role only the inventory-table privileges it needs.
- `services/storefront/scripts/init-secrets.py` and `init-secrets.ps1`: generate local AES and HMAC secret files without overwriting existing secrets.
- `services/storefront/compose.yaml` and `.env.example`: mount secret files into API and worker containers without exposing their values.
- `services/storefront/internal/orders/orders.go`, `views.go`, and `relations.go`: reserve codes during order creation, mark stock orders for the existing manual-checkout payment flow, and include delivery only for authorized customer detail reads.
- `services/storefront/internal/payments/service.go`: issue codes after verified payment; release reservations only after a payment provider confirms closure.
- `services/storefront/internal/httpapi/server.go`, `identity_management.go`, `orders.go`, and new `vouchers.go`: wire stock availability, callbacks, and admin-only inventory endpoints.
- `services/storefront/internal/catalog/catalog.go`: preserve the administrator's catalog-enabled switch while applying stock availability.
- `src/admin/VoucherInventoryPage.tsx`, `Admin.tsx`, and `admin.css`: add an admin stock screen with plan selection, text/file import, and counts only.
- `src/lib/use-store.ts`, `src/components/manual-checkout.tsx`, and `src/admin/OrderActionForm.tsx`: display an issued code in the buyer's authorized order detail and prevent marking an unissued stock order manually complete.

## Tasks

### Task 1: Add encrypted inventory ledger and configuration

**Files:**
- Create `services/storefront/migrations/034_voucher_inventory.sql`
- Create `services/storefront/internal/config/vouchers.go`
- Create `services/storefront/internal/vouchers/service.go`
- Modify `services/storefront/internal/store/store.go`
- Modify `services/storefront/scripts/init-secrets.py`
- Modify `services/storefront/scripts/init-secrets.ps1`
- Modify `services/storefront/compose.yaml`
- Modify `services/storefront/.env.example`

- [x] Add batch and voucher tables. Make HMAC fingerprints unique, restrict status to `available`, `reserved`, or `issued`, enforce one reserved/issued voucher per order, and store ciphertext/nonce separately from status metadata.
- [x] Load AES and HMAC key files through `config.Secret`; AES must decode to exactly 32 bytes and HMAC must decode to at least 32 bytes. Return configuration errors without including key content.
- [x] Implement `Service.Import(ctx, actor, plan string, raw []byte) (ImportResult, error)`. Parse and validate all lines first, trim surrounding whitespace, ignore blank lines, reject control characters, empty batches, more than 50 codes, overlong lines, batch duplicates, and persisted duplicates. Compute all HMAC fingerprints before beginning the transaction; on any duplicate or validation failure write zero rows and never echo a code.
- [x] Encrypt every accepted code with a fresh AES-GCM nonce. Insert a batch and all voucher rows in one transaction; write audit metadata with plan, batch ID, count, and actor only.
- [x] Add `Summary(ctx)`, `Availability(ctx, plans)`, `Managed(ctx, q, plan)`, `Reserve(ctx, tx, orderID, plan)`, `IssuePaid(ctx, tx, orderID)`, `ReleaseClosed(ctx, tx, orderID)`, and `CustomerDelivery(ctx, orderID)` service methods. Reserve with `FOR UPDATE SKIP LOCKED`; issuance is idempotent; release is only called for confirmed closure; late paid orders may atomically claim another available code or remain pending without a code.
- [x] Add runtime grants for voucher tables; update both local secret generators to create distinct random 32-byte secrets only when files do not already exist; mount the two files only in API and worker services.
- [x] Confirm code paths never log request bodies, decrypted values, import values, or fingerprints.

### Task 2: Connect checkout, payment settlement, and customer delivery

**Files:**
- Modify `services/storefront/internal/orders/orders.go`
- Modify `services/storefront/internal/orders/views.go`
- Modify `services/storefront/internal/orders/relations.go`
- Modify `services/storefront/internal/payments/service.go`
- Modify `services/storefront/internal/httpapi/server.go`
- Modify `services/storefront/internal/httpapi/identity_management.go`
- Modify `services/storefront/internal/httpapi/orders.go`
- Create `services/storefront/internal/httpapi/vouchers.go`

- [x] Add order-service callbacks for detecting stock-managed plans and reserving a voucher after the order row is created, inside the existing order transaction. Plans without voucher history keep their current behavior. A stock-managed plan with zero available stock rejects a new order.
- [x] Extend Go catalog availability so a stock-managed plan with no available codes is disabled; do not publish counts or codes in the public catalog.
- [x] Add payment callbacks to `payments.Service`. On a verified `paid` result, issue the existing reservation in the same DB transaction; if it was released or missing, try another available code. If none exists, leave the paid order waiting for manual fulfillment and record a redacted audit event. On provider-confirmed `closed`, cancel the order and release its reserved code in the same transaction. Do not release on local clock expiry alone.
- [x] Add customer detail delivery after order authorization. Only live, paid, unblocked, non-held, non-refunded orders can receive the code. Admin details and order lists never include it. Return the same issued code on repeated authorized reads.
- [x] Register `GET /api/admin/vouchers` and `POST /api/admin/vouchers/import` inside the existing authenticated admin group. Limit import request size, return batch/count/status only, and use generic errors that do not include code contents.
- [x] Keep API cache headers at `no-store`; ensure only buyer-session and customer-email ownership checks can read the delivered code.

### Task 3: Add admin inventory UI and customer order rendering

**Files:**
- Create `src/admin/VoucherInventoryPage.tsx`
- Modify `src/admin/Admin.tsx`
- Modify `src/admin/admin.css`
- Modify `src/lib/use-store.ts`
- Modify `src/components/checkout.tsx`
- Modify `src/components/manual-checkout.tsx`

- [x] Add a “卡密库存” admin navigation page. Fetch plan/status counts; select one plan; import pasted lines or a `.txt` file; show success/error status without echoing submitted codes; clear the textarea after successful import.
- [x] Display only product plan, batch timestamp/ID, imported count, and available/reserved/issued counts. Do not add stock export, plaintext readback, or a code reveal control.
- [x] Add an optional customer order delivery model with `status`, `code`, and `instructions`. Route stock-managed orders through `ManualCheckout`; render a copyable code only when the API has returned an issued voucher; pending delivery shows a neutral processing message without claiming success.
- [x] Avoid rendering voucher data in order-list rows, analytics, browser storage, or mail templates.
- [x] Prevent the generic manual-complete action from marking a stock order successful while it has no issued card.

### Task 4: Verify locally, document operations, and commit

**Files:**
- Modify `services/storefront/README.md`
- Modify `docs/superpowers/specs/2026-10-16-voucher-inventory-design.md` only if implementation discovery requires a design adjustment.

- [x] Document local key generation, required API/worker secret files, batch import behavior, no-code logging guarantees, and manual handling when a paid order has no available code.
- [x] Run `npm run build` from the repository root and `go build ./...` from `services/storefront`; do not run the test suites unless the user requests tests.
- [ ] Review the complete diff, run `git diff --check`, scan for secret/plaintext code leakage patterns, ensure `.release/` is not staged, and commit the completed feature on `codex/voucher-inventory`.
- [ ] Do not deploy until local build artifacts are ready and the server host, app directory, services, runtime database, and a restorable backup have been verified under the project server rules.

## Self-Review

- Spec coverage: schema, at-most-once allocation, zero-stock checkout block, payment callback idempotency, confirmed-closure release, late-payment recovery, encryption, admin-only imports/counts, customer-only code display, no-store responses, manual out-of-stock handling, and local-only builds map to Tasks 1–4.
- Placeholder scan: no TODO/TBD steps; all file paths, method names, endpoints, limits, and build commands are explicit.
- Type consistency: service methods use `context.Context`, pgx transaction/query interfaces, string plan/order IDs, and structured import/count/delivery results; HTTP and order/payment callbacks adapt those typed operations without returning plaintext outside authorized order detail.
