package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

// ErrLeased 表示账号已被其他调用方租约占用。
var ErrLeased = errors.New("账号已被占用")

// MaxCallerIDLen 是 caller_id 的长度上限。
//
// 足够放下 "worker-07"、主机名或一个 UUID，又不至于让人把整段日志塞进来。
const MaxCallerIDLen = 64

// NormalizeCallerID 归一化调用方标识。
//
// 只去首尾空白，不转小写 —— 与 project_key 不同，这里的值通常是主机名或
// 容器 ID，大小写是它本来的样子，改掉反而对不上运维手里的那份名单。
func NormalizeCallerID(s string) string { return strings.TrimSpace(s) }

// AcquireLease 为指定账号申请租约。
//
// 占用判定分两级。跨密钥一律拒绝，这是原有行为；同一把密钥之内，再比
// caller_id —— 多台机器共用一把密钥是最常见的部署方式，而它们彼此之间
// 原来是没有任何保护的。
//
// callerID 为空表示这次没自报身份，行为与升级前完全一致：同一把密钥可以续租。
// 身份是自愿提供的，但一旦提供，别人就拿不走 —— 这样老调用方不受影响，
// 新调用方只要开始带上它就立刻得到保护。
//
// 租约放数据库而不是 Redis：它需要持久化与审计，进程重启丢租约会让两个调用方拿到同一账号。
func (s *Store) AcquireLease(ctx context.Context, accountID, apiKeyID int64,
	callerID string, ttl time.Duration) (*model.Lease, error) {

	callerID = NormalizeCallerID(callerID)
	now := time.Now().Unix()
	exp := time.Now().Add(ttl).Unix()

	var l model.Lease
	err := s.queryRow(ctx,
		`SELECT account_id, api_key_id, acquired_at, expires_at, caller_id
		 FROM account_leases WHERE account_id = ?`,
		accountID).Scan(&l.AccountID, &l.APIKeyID, &l.AcquiredAt, &l.ExpiresAt, &l.CallerID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// 无租约，直接建立。
	case err != nil:
		return nil, err
	case l.ExpiresAt > now && l.APIKeyID != apiKeyID:
		return nil, ErrLeased
	case l.ExpiresAt > now && !sameCaller(l.CallerID, callerID):
		// 同一把密钥，但握在别的机器手里。
		return nil, ErrLeased
	}

	_, err = s.exec(ctx,
		`INSERT INTO account_leases (account_id, api_key_id, acquired_at, expires_at, caller_id)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT (account_id) DO UPDATE SET
		   api_key_id = excluded.api_key_id,
		   acquired_at = excluded.acquired_at,
		   expires_at = excluded.expires_at,
		   caller_id = excluded.caller_id`,
		accountID, apiKeyID, now, exp, callerID)
	if err != nil {
		return nil, err
	}
	return &model.Lease{
		AccountID: accountID, APIKeyID: apiKeyID,
		AcquiredAt: now, ExpiresAt: exp, CallerID: callerID,
	}, nil
}

// sameCaller 判断一次请求能否动这张租约。
//
// 租约上没有身份（升级前建的，或调用方没自报）时一律放行 —— 那时的保证
// 本来就只到密钥这一级，不能因为升级就把在途的租约锁死。
// 租约上有身份时必须对上：这正是自报身份换来的那份保护。
func sameCaller(leaseCaller, reqCaller string) bool {
	if leaseCaller == "" {
		return true
	}
	return leaseCaller == NormalizeCallerID(reqCaller)
}

// GetLease 返回账号当前的有效租约，没有则返回 nil。
func (s *Store) GetLease(ctx context.Context, accountID int64) (*model.Lease, error) {
	var l model.Lease
	err := s.queryRow(ctx,
		`SELECT account_id, api_key_id, acquired_at, expires_at, caller_id FROM account_leases
		 WHERE account_id = ? AND expires_at > ?`, accountID, time.Now().Unix()).
		Scan(&l.AccountID, &l.APIKeyID, &l.AcquiredAt, &l.ExpiresAt, &l.CallerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ReleaseLease 提前释放租约，只有持有者可以释放。
//
// 持有者同样是两级：密钥必须对上；租约上记了 caller_id 的，调用方也得对上。
// 租约不存在时静默成功，释放是幂等的 —— 重试一次不该报错。
func (s *Store) ReleaseLease(ctx context.Context, accountID, apiKeyID int64, callerID string) error {
	_, err := s.exec(ctx,
		`DELETE FROM account_leases
		 WHERE account_id = ? AND api_key_id = ? AND (caller_id = '' OR caller_id = ?)`,
		accountID, apiKeyID, NormalizeCallerID(callerID))
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
	// CallerID 是自报身份的调用方标识，写进租约。
	//
	// 多台机器共用一把密钥时，它决定了谁有权给这个账号收尾。留空即不启用。
	CallerID string
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
	// 「没有有效租约」用 NOT EXISTS 而不是 LEFT JOIN + IS NULL。
	//
	// 两种写法在 SQLite 上等价，在 PostgreSQL 上却差一个能不能跑：
	// 这条查询末尾要加 FOR UPDATE SKIP LOCKED 让多实例自动分工，而 PostgreSQL
	// 明确拒绝对外连接的可空一侧加行锁（FOR UPDATE cannot be applied to the
	// nullable side of an outer join）。也就是说 LEFT JOIN 那版在生产上
	// 根本不工作 —— 只在 SQLite 上测过，所以一直没人发现。
	q := `SELECT ` + accountCols + ` FROM accounts a
	      WHERE a.disabled = 0 AND a.status NOT IN ('INVALID','BANNED')
	        AND a.cooldown_until <= ?
	        AND NOT EXISTS (
	          SELECT 1 FROM account_leases l
	          WHERE l.account_id = a.id AND l.expires_at > ?)`
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
	lease, err := s.AcquireLease(ctx, acc.ID, opt.APIKeyID, opt.CallerID, opt.TTL)
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
