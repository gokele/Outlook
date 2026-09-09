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

// ClaimOptions 是领取账号的条件。
type ClaimOptions struct {
	CategoryID *int64
	APIKeyID   int64
	TTL        time.Duration
	// ProjectKey 非空时启用项目隔离：已经在这个项目上成功用过的账号不会被再次领取。
	//
	// 留空则退回原来的语义（只看租约），老的调用方不受影响。
	ProjectKey string
}

// ClaimFreeAccount 从指定分类中挑一个可领取的账号并加上租约。
//
// "可领取"要同时满足四条，缺一不可：
//
//   - 没有被其他调用方的租约占着
//   - 账号本身可用（未禁用、未失效、未封禁）
//   - 不在冷却期内 —— 刚失败过的账号大概率会接着失败
//   - 没有在这个项目上成功用过 —— 同一个邮箱在 A 站注册过就不能再注册 A 站
//
// 最后一条是项目隔离的全部内容。它按 project_key 维度记账，因此同一个邮箱
// 换个项目照样能用，这正是账号池能被复用的前提。
func (s *Store) ClaimFreeAccount(ctx context.Context, opt ClaimOptions) (*model.Account, *model.Lease, error) {
	now := time.Now().Unix()
	q := `SELECT ` + accountCols + ` FROM accounts a
	      LEFT JOIN account_leases l ON l.account_id = a.id AND l.expires_at > ?
	      WHERE a.disabled = 0 AND a.status NOT IN ('INVALID','BANNED')
	        AND l.account_id IS NULL AND a.cooldown_until <= ?`
	args := []any{now, now}
	if opt.CategoryID != nil {
		q += ` AND a.category_id = ?`
		args = append(args, *opt.CategoryID)
	}
	if key := NormalizeProjectKey(opt.ProjectKey); key != "" {
		// NOT EXISTS 而不是 LEFT JOIN + IS NULL：这张表按 (project_key, result)
		// 建了索引，NOT EXISTS 能直接命中，而且语义上更贴近"存在即排除"。
		q += ` AND NOT EXISTS (
		         SELECT 1 FROM account_projects p
		         WHERE p.account_id = a.id AND p.project_key = ? AND p.result = 'success')`
		args = append(args, key)
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
	lease, err := s.AcquireLease(ctx, acc.ID, opt.APIKeyID, opt.TTL)
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
