package store

import (
	"context"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// RotateTask 是调度队列里的一条待轮换任务。
type RotateTask struct {
	AccountID       int64
	Email           string
	ClientID        string
	Tenant          string
	RefreshTokenEnc []byte
	Priority        int   // 0 危急 1 紧急 2 正常 3 首验
	TokenExpiresAt  int64 // 0 表示从未轮换
	// ProxyID 是该账号已绑定的出口，0 表示尚未分配。
	// 在取任务时一并带出，调度层才能按出口分摊速率与并发 ——
	// 事后再逐个查库既慢，也来不及在派发前做打散。
	ProxyID int64
}

// ClaimRotateTasks 取出已到期的待轮换账号，按紧迫度排序。
//
// 优先级：P0 距硬到期不足 7 天，P1 不足 15 天，P2 正常，P3 从未轮换。
// 时间阈值在 Go 侧算好后传参，SQL 里不做日期运算，因此同一条查询在两种数据库上一致。
// PostgreSQL 追加 FOR UPDATE SKIP LOCKED 让多实例自动分工；
// SQLite 部署为单实例，调度器唯一，无需加锁。
func (s *Store) ClaimRotateTasks(ctx context.Context, limit int, suspended []string, includeP3 bool) ([]RotateTask, error) {
	now := time.Now()
	p0 := now.Add(7 * 24 * time.Hour).Unix()
	p1 := now.Add(15 * 24 * time.Hour).Unix()

	q := `SELECT id, email, client_id, tenant, refresh_token_enc, token_expires_at,
	        COALESCE(proxy_fallback_id, proxy_id, 0) AS px,
	        CASE
	          WHEN token_refreshed_at = 0    THEN 3
	          WHEN token_expires_at < ?      THEN 0
	          WHEN token_expires_at < ?      THEN 1
	          ELSE 2
	        END AS prio
	      FROM accounts
	      WHERE disabled = 0
	        AND status <> 'INVALID'
	        AND next_rotate_at <= ?`
	args := []any{p0, p1, now.Unix()}

	if !includeP3 {
		q += ` AND token_refreshed_at <> 0`
	}
	if len(suspended) > 0 {
		q += ` AND client_id NOT IN (` + placeholders(len(suspended)) + `)`
		for _, c := range suspended {
			args = append(args, c)
		}
	}
	q += ` ORDER BY prio ASC, token_expires_at ASC LIMIT ?` + s.forUpdateSkipLocked()
	args = append(args, limit)

	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RotateTask
	for rows.Next() {
		var t RotateTask
		if err := rows.Scan(&t.AccountID, &t.Email, &t.ClientID, &t.Tenant,
			&t.RefreshTokenEnc, &t.ProxyID, &t.TokenExpiresAt, &t.Priority); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// QueueStats 是调度队列的健康度快照。
type QueueStats struct {
	Backlog    int `json:"backlog"`     // 已到期待处理数
	P0         int `json:"p0"`          // 距硬到期不足 7 天，应恒为 0
	P1         int `json:"p1"`          // 距硬到期不足 15 天
	Unverified int `json:"unverified"`  // 从未轮换
	NeedManual int `json:"need_manual"` // 连续失败 5 次以上
}

// SchedulerStats 统计队列健康度，供总览页与容量自检使用。
func (s *Store) SchedulerStats(ctx context.Context) (QueueStats, error) {
	now := time.Now()
	var st QueueStats
	q := func(dst *int, where string, args ...any) error {
		return s.queryRow(ctx, `SELECT COUNT(*) FROM accounts WHERE disabled = 0 AND status <> 'INVALID' AND `+where, args...).Scan(dst)
	}
	if err := q(&st.Backlog, `next_rotate_at <= ?`, now.Unix()); err != nil {
		return st, err
	}
	if err := q(&st.P0, `token_refreshed_at <> 0 AND token_expires_at < ?`, now.Add(7*24*time.Hour).Unix()); err != nil {
		return st, err
	}
	if err := q(&st.P1, `token_refreshed_at <> 0 AND token_expires_at < ?`, now.Add(15*24*time.Hour).Unix()); err != nil {
		return st, err
	}
	if err := q(&st.Unverified, `token_refreshed_at = 0`); err != nil {
		return st, err
	}
	if err := q(&st.NeedManual, `rotate_fail_count >= 5`); err != nil {
		return st, err
	}
	return st, nil
}

// PushNextRotate 直接设置某账号的下次轮换时间，用于取件顺带轮换后同步队列。
func (s *Store) PushNextRotate(ctx context.Context, id int64, at int64) error {
	_, err := s.exec(ctx, `UPDATE accounts SET next_rotate_at = ? WHERE id = ?`, at, id)
	return err
}

// SampleUnverified 随机取若干未验证账号，用于导入后的抽样验证。
func (s *Store) SampleUnverified(ctx context.Context, n int) ([]RotateTask, error) {
	order := "RANDOM()" // 两种数据库都支持 RANDOM()
	rows, err := s.query(ctx,
		`SELECT id, email, client_id, tenant, refresh_token_enc, token_expires_at, 3
		 FROM accounts
		 WHERE disabled = 0 AND status = 'UNVERIFIED'
		 ORDER BY `+order+` LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RotateTask
	for rows.Next() {
		var t RotateTask
		if err := rows.Scan(&t.AccountID, &t.Email, &t.ClientID, &t.Tenant,
			&t.RefreshTokenEnc, &t.TokenExpiresAt, &t.Priority); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ChannelPolicyOrder 返回该账号实际要尝试的通道顺序。
// 账号显式指定某条通道时只用那一条，否则按全局顺序并跳过已知不可用的通道。
func ChannelPolicyOrder(a *model.Account, global []model.Channel) []model.Channel {
	if a.ChannelPolicy != "" && a.ChannelPolicy != "auto" {
		return []model.Channel{model.Channel(a.ChannelPolicy)}
	}
	var out []model.Channel
	for _, ch := range global {
		if ok, probed := a.Capabilities.Get(ch); probed && !ok {
			continue // 已确认不可用，跳过
		}
		out = append(out, ch)
	}
	if len(out) == 0 {
		out = append(out, global...)
	}
	return out
}

// EarliestExpiry 返回最早到达 90 天硬到期的时间，用于在调度器被关闭时
// 向用户展示第一个账号预计失效的日期。没有已轮换账号时返回 0。
func (s *Store) EarliestExpiry(ctx context.Context) (int64, error) {
	var ts int64
	err := s.queryRow(ctx,
		`SELECT COALESCE(MIN(token_expires_at), 0) FROM accounts
		 WHERE disabled = 0 AND status <> 'INVALID' AND token_refreshed_at <> 0`).Scan(&ts)
	return ts, err
}
