package vouchers

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"gptibo/storefront/internal/catalog"
	"gptibo/storefront/internal/fault"
	"gptibo/storefront/internal/security"
	"gptibo/storefront/internal/store"
)

const (
	maxBatchSize      = 50
	maxCodeBytes      = 1024
	reservationWindow = 15 * time.Minute
)

type Query interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Service struct {
	Store          *store.Store
	aead           cipher.AEAD
	fingerprintKey []byte
}

type ImportResult struct {
	BatchID string `json:"batchId"`
	PlanID  string `json:"planId"`
	Count   int    `json:"count"`
}

type PlanSummary struct {
	ID        string `json:"id"`
	ProductID string `json:"productId"`
	Name      string `json:"name"`
	Available int    `json:"available"`
	Reserved  int    `json:"reserved"`
	Issued    int    `json:"issued"`
}

type BatchSummary struct {
	ID        string `json:"id"`
	PlanID    string `json:"planId"`
	PlanName  string `json:"planName"`
	Count     int    `json:"count"`
	Available int    `json:"available"`
	Reserved  int    `json:"reserved"`
	Issued    int    `json:"issued"`
	CreatedAt int64  `json:"createdAt"`
}

type InventorySummary struct {
	Configured bool           `json:"configured"`
	Plans      []PlanSummary  `json:"plans"`
	Batches    []BatchSummary `json:"batches"`
}

type Delivery struct {
	Status       string `json:"status"`
	Code         string `json:"code,omitempty"`
	Instructions string `json:"instructions"`
}

type encryptedCode struct {
	fingerprint []byte
	nonce       []byte
	ciphertext  []byte
}

func New(s *store.Store, encryptionKey, fingerprintKey []byte) (*Service, error) {
	v := &Service{Store: s}
	if len(encryptionKey) == 0 && len(fingerprintKey) == 0 {
		return v, nil
	}
	if len(encryptionKey) != 32 || len(fingerprintKey) < 32 {
		return nil, errors.New("invalid voucher key configuration")
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, errors.New("invalid voucher encryption key")
	}
	v.aead, err = cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("voucher encryption is unavailable")
	}
	v.fingerprintKey = append([]byte(nil), fingerprintKey...)
	return v, nil
}

func (s *Service) Configured() bool { return s != nil && s.aead != nil && len(s.fingerprintKey) >= 32 }

func (s *Service) fingerprint(code string) []byte {
	h := hmac.New(sha256.New, s.fingerprintKey)
	_, _ = h.Write([]byte(code))
	return h.Sum(nil)
}

func (s *Service) encrypt(code string) (encryptedCode, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return encryptedCode{}, errors.New("voucher encryption is unavailable")
	}
	return encryptedCode{fingerprint: s.fingerprint(code), nonce: nonce, ciphertext: s.aead.Seal(nil, nonce, []byte(code), nil)}, nil
}

func (s *Service) decrypt(nonce, ciphertext []byte) (string, error) {
	if !s.Configured() || len(nonce) != s.aead.NonceSize() {
		return "", errors.New("voucher decryption is unavailable")
	}
	plain, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil || !utf8.Valid(plain) {
		return "", errors.New("voucher decryption is unavailable")
	}
	return string(plain), nil
}

func (s *Service) requireKeys() error {
	if !s.Configured() {
		return fault.New(503, "卡密库存密钥尚未配置，暂时无法操作库存。")
	}
	return nil
}

func (s *Service) Import(ctx context.Context, actor, plan string, raw []byte) (ImportResult, error) {
	var out ImportResult
	if err := s.requireKeys(); err != nil {
		return out, err
	}
	if len(raw) == 0 || len(raw) > 32768 {
		return out, fault.New(400, "导入内容为空或过大。")
	}
	seen := map[string]bool{}
	prepared := make([]encryptedCode, 0, maxBatchSize)
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		code := strings.TrimSpace(line)
		if code == "" {
			continue
		}
		if !utf8.ValidString(code) || len(code) > maxCodeBytes || strings.IndexFunc(code, unicode.IsControl) >= 0 {
			return out, fault.New(400, "导入失败：存在格式无效的卡密；未写入任何内容。")
		}
		if len(prepared) >= maxBatchSize {
			return out, fault.New(400, "单批最多导入 50 条卡密；未写入任何内容。")
		}
		fingerprint := string(s.fingerprint(code))
		if seen[fingerprint] {
			return out, fault.New(409, "导入失败：批次中存在重复卡密；未写入任何内容。")
		}
		seen[fingerprint] = true
		item, err := s.encrypt(code)
		if err != nil {
			return out, fault.New(503, "卡密加密失败，未写入任何内容。")
		}
		prepared = append(prepared, item)
	}
	if len(prepared) == 0 {
		return out, fault.New(400, "导入内容中没有可用卡密。")
	}
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var planExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_plans WHERE id=$1)`, plan).Scan(&planExists); err != nil {
		return out, err
	}
	if !planExists {
		return out, fault.New(404, "未找到所选套餐。")
	}
	for _, item := range prepared {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM voucher_inventory WHERE fingerprint=$1)`, item.fingerprint).Scan(&exists); err != nil {
			return out, err
		}
		if exists {
			return out, fault.New(409, "导入失败：存在已导入过的卡密；未写入任何内容。")
		}
	}
	batchID := security.ID()
	now := time.Now().UnixMilli()
	if _, err = tx.Exec(ctx, `INSERT INTO voucher_batches(id,plan_id,imported_by,imported_count,created_at) VALUES($1,$2,$3,$4,$5)`, batchID, plan, actor, len(prepared), now); err != nil {
		return out, err
	}
	for _, item := range prepared {
		if _, err = tx.Exec(ctx, `INSERT INTO voucher_inventory(id,batch_id,plan_id,fingerprint,ciphertext,nonce,status,created_at,imported_by) VALUES($1,$2,$3,$4,$5,$6,'available',$7,$8)`, security.ID(), batchID, plan, item.fingerprint, item.ciphertext, item.nonce, now, actor); err != nil {
			return out, err
		}
	}
	detail, _ := json.Marshal(map[string]any{"planId": plan, "batchId": batchID, "count": len(prepared)})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,actor_role,actor_id,action,target,detail) VALUES($1,'admin',$2,'voucher-import',$3,$4)`, security.ID(), actor, plan, detail); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return ImportResult{BatchID: batchID, PlanID: plan, Count: len(prepared)}, nil
}

func (s *Service) Summary(ctx context.Context) (InventorySummary, error) {
	out := InventorySummary{Configured: s.Configured(), Plans: []PlanSummary{}, Batches: []BatchSummary{}}
	rows, err := s.Store.DB.Query(ctx, `SELECT c.id,c.product_id,c.name,count(v.id) FILTER(WHERE v.status='available')::int,count(v.id) FILTER(WHERE v.status='reserved')::int,count(v.id) FILTER(WHERE v.status='issued')::int FROM catalog_plans c LEFT JOIN voucher_inventory v ON v.plan_id=c.id GROUP BY c.id ORDER BY c.sort_order,c.id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p PlanSummary
		if err = rows.Scan(&p.ID, &p.ProductID, &p.Name, &p.Available, &p.Reserved, &p.Issued); err != nil {
			rows.Close()
			return out, err
		}
		out.Plans = append(out.Plans, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.Store.DB.Query(ctx, `SELECT b.id,b.plan_id,c.name,b.imported_count,count(v.id) FILTER(WHERE v.status='available')::int,count(v.id) FILTER(WHERE v.status='reserved')::int,count(v.id) FILTER(WHERE v.status='issued')::int,b.created_at FROM voucher_batches b JOIN catalog_plans c ON c.id=b.plan_id LEFT JOIN voucher_inventory v ON v.batch_id=b.id GROUP BY b.id,c.name ORDER BY b.created_at DESC,b.id DESC LIMIT 500`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b BatchSummary
		if err = rows.Scan(&b.ID, &b.PlanID, &b.PlanName, &b.Count, &b.Available, &b.Reserved, &b.Issued, &b.CreatedAt); err != nil {
			return out, err
		}
		out.Batches = append(out.Batches, b)
	}
	return out, rows.Err()
}

func (s *Service) Availability(ctx context.Context, plans []catalog.Plan) []catalog.Plan {
	out := append([]catalog.Plan(nil), plans...)
	for i := range out {
		var managed bool
		var available int
		err := s.Store.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM voucher_inventory WHERE plan_id=$1),count(*) FILTER(WHERE status='available')::int FROM voucher_inventory WHERE plan_id=$1`, out[i].ID).Scan(&managed, &available)
		if err != nil {
			out[i].Enabled = 0
		} else if managed {
			if !out[i].CatalogEnabled || available == 0 || !s.Configured() {
				out[i].Enabled = 0
			} else {
				out[i].Enabled = 1
			}
		}
	}
	return out
}

func (s *Service) Managed(ctx context.Context, q Query, plan string) (bool, error) {
	var managed bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM voucher_inventory WHERE plan_id=$1)`, plan).Scan(&managed)
	return managed, err
}

func (s *Service) Reserve(ctx context.Context, tx pgx.Tx, orderID, plan string) error {
	managed, err := s.Managed(ctx, tx, plan)
	if err != nil || !managed {
		return err
	}
	if err = s.requireKeys(); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM voucher_inventory WHERE plan_id=$1 AND status='available' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, plan)
	if err != nil {
		return err
	}
	var id string
	if rows.Next() {
		err = rows.Scan(&id)
	} else {
		err = pgx.ErrNoRows
	}
	rows.Close()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.New(409, "该套餐卡密库存不足，暂时不能下单。")
		}
		return err
	}
	now := time.Now().UnixMilli()
	result, err := tx.Exec(ctx, `UPDATE voucher_inventory SET status='reserved',order_id=$1,reserved_until=$2,reserved_at=$3 WHERE id=$4 AND status='available'`, orderID, now+reservationWindow.Milliseconds(), now, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fault.New(409, "卡密库存状态已变化，请重新下单。")
	}
	return nil
}

func (s *Service) IssuePaid(ctx context.Context, tx pgx.Tx, orderID string) error {
	var plan, mode, payment, status string
	var hold, blocked int
	var refunded int64
	if err := tx.QueryRow(ctx, `SELECT plan,mode,payment_status,status,refund_hold,redemption_blocked,refunded_cents FROM orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&plan, &mode, &payment, &status, &hold, &blocked, &refunded); err != nil {
		return err
	}
	managed, err := s.Managed(ctx, tx, plan)
	if err != nil || !managed {
		return err
	}
	if mode != "live" || payment != "paid" || status != "session" || hold != 0 || blocked != 0 || refunded != 0 {
		return nil
	}
	if !s.Configured() {
		return s.recordAttention(ctx, tx, orderID, plan, "key-unavailable")
	}
	var id, current string
	err = tx.QueryRow(ctx, `SELECT id,status FROM voucher_inventory WHERE order_id=$1 FOR UPDATE`, orderID).Scan(&id, &current)
	if err == nil && current == "issued" {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && current == "reserved" {
		_, err = tx.Exec(ctx, `UPDATE voucher_inventory SET status='issued',reserved_until=NULL,issued_at=$1 WHERE id=$2 AND status='reserved'`, time.Now().UnixMilli(), id)
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM voucher_inventory WHERE plan_id=$1 AND status='available' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, plan)
	if err != nil {
		return err
	}
	if rows.Next() {
		err = rows.Scan(&id)
	} else {
		err = pgx.ErrNoRows
	}
	rows.Close()
	if errors.Is(err, pgx.ErrNoRows) {
		return s.recordAttention(ctx, tx, orderID, plan, "stock-empty")
	}
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	result, err := tx.Exec(ctx, `UPDATE voucher_inventory SET status='issued',order_id=$1,reserved_until=NULL,reserved_at=$2,issued_at=$2 WHERE id=$3 AND status='available'`, orderID, now, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return s.recordAttention(ctx, tx, orderID, plan, "allocation-conflict")
	}
	return nil
}

func (s *Service) ReleaseClosed(ctx context.Context, tx pgx.Tx, orderID string) error {
	var confirmed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE id=$1 AND payment_status='unpaid' AND status='cancelled')`, orderID).Scan(&confirmed); err != nil {
		return err
	}
	if !confirmed {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE voucher_inventory SET status='available',order_id=NULL,reserved_until=NULL,reserved_at=NULL WHERE order_id=$1 AND status='reserved'`, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	return s.recordAudit(ctx, tx, "system", "voucher-reservation-released", orderID, map[string]any{})
}

func (s *Service) CustomerDelivery(ctx context.Context, orderID string) (*Delivery, error) {
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var plan, mode, payment string
	var hold, blocked int
	var refunded int64
	var instructions string
	err = tx.QueryRow(ctx, `SELECT o.plan,o.mode,o.payment_status,o.refund_hold,o.redemption_blocked,o.refunded_cents,c.delivery_instructions FROM orders o JOIN catalog_plans c ON c.id=o.plan WHERE o.id=$1`, orderID).Scan(&plan, &mode, &payment, &hold, &blocked, &refunded, &instructions)
	if err != nil {
		return nil, err
	}
	managed, err := s.Managed(ctx, tx, plan)
	if err != nil || !managed || mode != "live" {
		return nil, err
	}
	if instructions == "" {
		instructions = "请按商品页面说明兑换；如遇问题，请联系商店支持。"
	}
	if payment != "paid" || hold != 0 || blocked != 0 || refunded != 0 {
		return &Delivery{Status: "waiting", Instructions: instructions}, nil
	}
	if !s.Configured() {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &Delivery{Status: "pending", Instructions: instructions}, nil
	}
	if err = s.IssuePaid(ctx, tx, orderID); err != nil {
		return nil, err
	}
	var nonce, ciphertext []byte
	err = tx.QueryRow(ctx, `SELECT nonce,ciphertext FROM voucher_inventory WHERE order_id=$1 AND status='issued'`, orderID).Scan(&nonce, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &Delivery{Status: "pending", Instructions: instructions}, nil
	}
	if err != nil {
		return nil, err
	}
	code, err := s.decrypt(nonce, ciphertext)
	if err != nil {
		return nil, fault.New(503, "卡密暂时无法解密，请联系商店支持。")
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &Delivery{Status: "issued", Code: code, Instructions: instructions}, nil
}

func (s *Service) recordAttention(ctx context.Context, tx pgx.Tx, orderID, plan, reason string) error {
	tag, err := tx.Exec(ctx, `INSERT INTO voucher_manual_attention(order_id,plan_id,reason,created_at) VALUES($1,$2,$3,$4) ON CONFLICT(order_id) DO NOTHING`, orderID, plan, reason, time.Now().UnixMilli())
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return s.recordAudit(ctx, tx, "payment", "voucher-manual-attention", orderID, map[string]any{"planId": plan, "reason": reason})
}

func (s *Service) recordAudit(ctx context.Context, tx pgx.Tx, actor, action, target string, detail any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,actor_role,actor_id,action,target,detail) VALUES($1,'system',$2,$3,$4,$5)`, security.ID(), actor, action, target, b)
	return err
}
