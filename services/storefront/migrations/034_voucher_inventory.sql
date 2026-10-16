CREATE TABLE voucher_batches (
    id uuid PRIMARY KEY,
    plan_id text NOT NULL REFERENCES catalog_plans(id),
    imported_by text NOT NULL,
    imported_count integer NOT NULL CHECK (imported_count BETWEEN 1 AND 50),
    created_at bigint NOT NULL
);

CREATE TABLE voucher_manual_attention (
    order_id text PRIMARY KEY REFERENCES orders(id),
    plan_id text NOT NULL REFERENCES catalog_plans(id),
    reason text NOT NULL CHECK (reason IN ('key-unavailable','stock-empty','allocation-conflict')),
    created_at bigint NOT NULL
);

CREATE TABLE voucher_inventory (
    id uuid PRIMARY KEY,
    batch_id uuid NOT NULL REFERENCES voucher_batches(id),
    plan_id text NOT NULL REFERENCES catalog_plans(id),
    fingerprint bytea NOT NULL UNIQUE CHECK (octet_length(fingerprint)=32),
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
    status text NOT NULL CHECK (status IN ('available','reserved','issued')),
    order_id text REFERENCES orders(id),
    reserved_until bigint,
    created_at bigint NOT NULL,
    reserved_at bigint,
    issued_at bigint,
    imported_by text NOT NULL,
    CHECK (
      (status='available' AND order_id IS NULL AND reserved_until IS NULL AND reserved_at IS NULL AND issued_at IS NULL)
      OR (status='reserved' AND order_id IS NOT NULL AND reserved_until IS NOT NULL AND reserved_at IS NOT NULL AND issued_at IS NULL)
      OR (status='issued' AND order_id IS NOT NULL AND reserved_until IS NULL AND reserved_at IS NOT NULL AND issued_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX voucher_inventory_order ON voucher_inventory(order_id) WHERE order_id IS NOT NULL;
CREATE INDEX voucher_inventory_plan_status ON voucher_inventory(plan_id,status,created_at,id);
CREATE INDEX voucher_inventory_batch ON voucher_inventory(batch_id,status);
