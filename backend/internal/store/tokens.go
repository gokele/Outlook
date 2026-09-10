package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

// LoadAccessToken 读取某账号在某 scope 下缓存的访问令牌。
// 第二个返回值为假表示没有缓存或已过期。
func (s *Store) LoadAccessToken(ctx context.Context, accountID int64, scope string) (*model.AccessToken, bool, error) {
	var t model.AccessToken
	err := s.queryRow(ctx,
		`SELECT account_id, scope, access_token_enc, expires_at, updated_at
		 FROM account_tokens WHERE account_id = ? AND scope = ?`, accountID, scope).
		Scan(&t.AccountID, &t.Scope, &t.AccessTokenEnc, &t.ExpiresAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if t.ExpiresAt <= time.Now().Unix() {
		return nil, false, nil
	}
	return &t, true, nil
}

// UpsertAccessToken 写入访问令牌。这是取件热路径上唯一的写入点。
func (s *Store) UpsertAccessToken(ctx context.Context, accountID int64, scope string, enc []byte, expiresAt int64) error {
	_, err := s.exec(ctx,
		`INSERT INTO account_tokens (account_id, scope, access_token_enc, expires_at, updated_at)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT (account_id, scope) DO UPDATE SET
		   access_token_enc = excluded.access_token_enc,
		   expires_at = excluded.expires_at,
		   updated_at = excluded.updated_at`,
		accountID, scope, enc, expiresAt, time.Now().Unix())
	return err
}

// RotateResult 是一次成功轮换后要落库的全部状态。
type RotateResult struct {
	AccountID       int64
	RefreshTokenEnc []byte
	RefreshedAt     int64
	ExpiresAt       int64 // token_refreshed_at + 90 天
	NextRotateAt    int64
	Scope           string
	AccessTokenEnc  []byte
	AccessExpiresAt int64
	Channel         model.Channel
}

// CommitRotation 在一个事务里写回轮换结果：新的授权码、时间基线、状态与通道能力，
// 以及本次顺带取到的访问令牌。任一步失败则整体回滚，不会出现写了令牌没写状态的中间态。
func (s *Store) CommitRotation(ctx context.Context, r RotateResult) error {
	return s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.exec(ctx,
			`UPDATE accounts SET refresh_token_enc = ?, token_refreshed_at = ?, token_expires_at = ?,
			   next_rotate_at = ?, status = 'ACTIVE', rotate_fail_count = 0, last_error = ''
			 WHERE id = ?`,
			r.RefreshTokenEnc, r.RefreshedAt, r.ExpiresAt, r.NextRotateAt, r.AccountID); err != nil {
			return err
		}
		if _, err := tx.exec(ctx,
			`INSERT INTO account_tokens (account_id, scope, access_token_enc, expires_at, updated_at)
			 VALUES (?,?,?,?,?)
			 ON CONFLICT (account_id, scope) DO UPDATE SET
			   access_token_enc = excluded.access_token_enc,
			   expires_at = excluded.expires_at,
			   updated_at = excluded.updated_at`,
			r.AccountID, r.Scope, r.AccessTokenEnc, r.AccessExpiresAt, time.Now().Unix()); err != nil {
			return err
		}
		return tx.setCapability(ctx, r.AccountID, r.Channel, true)
	})
}

// setCapability 在事务内记录通道可用性。
func (t *Tx) setCapability(ctx context.Context, id int64, ch model.Channel, ok bool) error {
	var caps string
	if err := t.queryRow(ctx, `SELECT capabilities FROM accounts WHERE id = ?`, id).Scan(&caps); err != nil {
		return err
	}
	var c model.Capabilities
	if caps != "" {
		_ = jsonUnmarshal(caps, &c)
	}
	c.Set(ch, ok)
	b, _ := jsonMarshal(c)
	_, err := t.exec(ctx, `UPDATE accounts SET capabilities = ? WHERE id = ?`, b, id)
	return err
}

// MarkInvalid 把账号置为失效并记录原因。只有微软确认的认证失败才会走到这里。
func (s *Store) MarkInvalid(ctx context.Context, id int64, reason string) error {
	return s.MarkFailed(ctx, id, model.StatusInvalid, reason, "")
}

// MarkFailed 把账号置为某个失败态，并记下机器可读的错误标识。
//
// status 只接受 INVALID 与 BANNED 两种：前者重新导入授权码能救，
// 后者不能。分开记是为了让运维一眼看出哪些还值得抢救，
// 也让调度器不去反复撞一个永远不会成功的账号。
func (s *Store) MarkFailed(ctx context.Context, id int64,
	status model.AccountStatus, reason, code string) error {

	if len(reason) > 500 {
		reason = reason[:500]
	}
	if status != model.StatusBanned {
		status = model.StatusInvalid
	}
	_, err := s.exec(ctx,
		`UPDATE accounts SET status = ?, last_error = ?, last_error_code = ? WHERE id = ?`,
		string(status), reason, code, id)
	return err
}

// MarkExpiring 把已过轮换点但尚未失效的账号标为待轮换。
func (s *Store) MarkExpiring(ctx context.Context, id int64) error {
	_, err := s.exec(ctx,
		`UPDATE accounts SET status = 'EXPIRING' WHERE id = ? AND status = 'ACTIVE'`, id)
	return err
}

// BumpRotateFailure 记录一次轮换失败并按退避推后下次尝试。
// 只有网络类与限流类失败才调用这里，认证失败直接置 INVALID。
// DeferRotate 把下次轮换推后若干秒，不计失败也不写最近错误。
//
// 与 BumpRotateFailure 的分野是这套设计的关键: 出口代理不可用属于
// "暂时做不了"，账号本身没问题。若按失败处理会累加 rotate_fail_count，
// 触发指数退避，最终把一批健康账号判成失效 —— 那是代理故障不该有的代价。
func (s *Store) DeferRotate(ctx context.Context, id int64, seconds int64) error {
	if seconds <= 0 {
		seconds = 60
	}
	next := time.Now().Add(time.Duration(seconds) * time.Second).Unix()
	// 只在推后时更新: 已经排在更晚的账号不该被提前。
	_, err := s.exec(ctx,
		`UPDATE accounts SET next_rotate_at = ? WHERE id = ? AND next_rotate_at < ?`,
		next, id, next)
	return err
}

func (s *Store) BumpRotateFailure(ctx context.Context, id int64, reason string) error {
	a, err := s.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	n := a.RotateFailCount + 1
	next := time.Now().Add(backoffFor(n)).Unix()
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err = s.exec(ctx,
		`UPDATE accounts SET rotate_fail_count = ?, next_rotate_at = ?, last_error = ? WHERE id = ?`,
		n, next, reason, id)
	return err
}

// backoffFor 返回第 n 次连续失败后的等待时长。
// 1 小时、6 小时、1 天、3 天，之后固定 7 天并在界面上标记需人工介入。
func backoffFor(n int) time.Duration {
	switch {
	case n <= 1:
		return time.Hour
	case n == 2:
		return 6 * time.Hour
	case n == 3:
		return 24 * time.Hour
	case n == 4:
		return 72 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

// ResetInvalidToUnverified 把一批账号从失效回滚为未验证。
// 用于 client_id 熔断后修正被误判的账号。
func (s *Store) ResetInvalidToUnverified(ctx context.Context, clientID string, since int64) (int64, error) {
	res, err := s.exec(ctx,
		`UPDATE accounts SET status = 'UNVERIFIED', rotate_fail_count = 0, next_rotate_at = ?, last_error = ''
		 WHERE client_id = ? AND status = 'INVALID'`, time.Now().Unix(), clientID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
