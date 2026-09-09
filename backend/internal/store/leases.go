package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// ErrLeased 表示账号已被其他调用方租约占用。
var ErrLeased = errors.New("账号已被占用")

// AcquireLease 为指定账号申请租约。同一个 Key 可以续租，其他 Key 在租约有效期内会被拒绝。
// 租约放数据库而不是 Redis：它需要持久化与审计，进程重启丢租约会让两个调用方拿到同一账号。
func (s *Store) AcquireLease(ctx context.Context, accountID, apiKeyID int64, ttl time.Duration) (*model.Lease, error) {
	now := time.Now().Unix()
	exp := time.Now().Add(ttl).Unix()

	var l model.Lease
	err := s.queryRow(ctx,
		`SELECT account_id, api_key_id, acquired_at, expires_at FROM account_leases WHERE account_id = ?`,
		accountID).Scan(&l.AccountID, &l.APIKeyID, &l.AcquiredAt, &l.ExpiresAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// 无租约，直接建立。
	case err != nil:
		return nil, err
	case l.ExpiresAt > now && l.APIKeyID != apiKeyID:
		return nil, ErrLeased
	}

	_, err = s.exec(ctx,
		`INSERT INTO account_leases (account_id, api_key_id, acquired_at, expires_at)
		 VALUES (?,?,?,?)
		 ON CONFLICT (account_id) DO UPDATE SET
		   api_key_id = excluded.api_key_id,
		   acquired_at = excluded.acquired_at,
		   expires_at = excluded.expires_at`,
		accountID, apiKeyID, now, exp)
	if err != nil {
		return nil, err
	}
	return &model.Lease{AccountID: accountID, APIKeyID: apiKeyID, AcquiredAt: now, ExpiresAt: exp}, nil
}

// GetLease 返回账号当前的有效租约，没有则返回 nil。
func (s *Store) GetLease(ctx context.Context, accountID int64) (*model.Lease, error) {
	var l model.Lease
	err := s.queryRow(ctx,
		`SELECT account_id, api_key_id, acquired_at, expires_at FROM account_leases
		 WHERE account_id = ? AND expires_at > ?`, accountID, time.Now().Unix()).
		Scan(&l.AccountID, &l.APIKeyID, &l.AcquiredAt, &l.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ReleaseLease 提前释放租约，只有持有者可以释放。
func (s *Store) ReleaseLease(ctx context.Context, accountID, apiKeyID int64) error {
	_, err := s.exec(ctx,
		`DELETE FROM account_leases WHERE account_id = ? AND api_key_id = ?`, accountID, apiKeyID)
	return err
}

// ClaimFreeAccount 从指定分类中挑一个未被占用、可用的账号并加上租约。
// 用于多调用方共享账号池的场景。
func (s *Store) ClaimFreeAccount(ctx context.Context, categoryID *int64, apiKeyID int64, ttl time.Duration) (*model.Account, *model.Lease, error) {
	now := time.Now().Unix()
	q := `SELECT ` + accountCols + ` FROM accounts a
	      LEFT JOIN account_leases l ON l.account_id = a.id AND l.expires_at > ?
	      WHERE a.disabled = 0 AND a.status <> 'INVALID' AND l.account_id IS NULL`
	args := []any{now}
	if categoryID != nil {
		q += ` AND a.category_id = ?`
		args = append(args, *categoryID)
	}
	q += ` ORDER BY a.last_fetch_at ASC LIMIT 1` + s.forUpdateSkipLocked()

	row := s.queryRow(ctx, q, args...)
	acc, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	lease, err := s.AcquireLease(ctx, acc.ID, apiKeyID, ttl)
	if err != nil {
		return nil, nil, err
	}
	return acc, lease, nil
}

// PurgeExpiredLeases 清理过期租约。
func (s *Store) PurgeExpiredLeases(ctx context.Context) error {
	_, err := s.exec(ctx, `DELETE FROM account_leases WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
