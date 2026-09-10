package store

import (
	"context"
	"time"

	"github.com/gokele/Outlook/internal/model"
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

// rotateTaskCols 是取任务时要读的列。prio 由调用方按档次填常量，不在 SQL 里算。
const rotateTaskCols = `id, email, client_id, tenant, refresh_token_enc, token_expires_at,
 COALESCE(proxy_fallback_id, proxy_id, 0)`

// ClaimRotateTasks 取出已到期的待轮换账号，按紧迫度排序。
//
// 优先级：P0 距硬到期不足 7 天，P1 不足 15 天，P2 正常，P3 从未轮换。
// 时间阈值在 Go 侧算好后传参，SQL 里不做日期运算，因此同一条查询在两种数据库上一致。
// PostgreSQL 追加 FOR UPDATE SKIP LOCKED 让多实例自动分工；
// SQLite 部署为单实例，调度器唯一，无需加锁。
//
// **四个档次分四条查询取，而不是一条查询加 CASE 算优先级。**
// 原来那种写法要先给每一条到期记录算出 prio，再对全部结果排序，最后只要前 N 条。
// 十万账号时排的是几千行，看不出问题；十亿账号每天到期一千六百万行，
// 就是为了拿 30 条任务而对一千六百万行做一次排序 —— 每分钟一次。
//
// 拆开之后每一档都是"沿着某个索引扫，够 N 条就停"，代价与 limit 成正比，
// 与表有多大无关。档次之间天然有序，拼起来就是原来的排序结果。
func (s *Store) ClaimRotateTasks(ctx context.Context, limit int, suspended []string, includeP3 bool) ([]RotateTask, error) {
	now := time.Now()
	nowUnix := now.Unix()
	p0 := now.Add(7 * 24 * time.Hour).Unix()
	p1 := now.Add(15 * 24 * time.Hour).Unix()

	// 每一档的条件与排序键。排序键都选成能直接吃上索引的那一列：
	// 前两档按硬到期时间（idx_accounts_expiry），P2 按下次轮换时间
	// （idx_accounts_rotate），P3 按 id（idx_accounts_unverified）。
	//
	// P2 改用 next_rotate_at 排序是唯一一处语义变化，而且是等价的：
	// 这一档里所有账号距硬到期都还有 15 天以上，彼此之间谁先谁后无关紧要，
	// 而 next_rotate_at 最小的就是等得最久的那个 —— 先做它同样是对的。
	tiers := []struct {
		prio  int
		cond  string
		args  []any
		order string
	}{
		{0, `token_refreshed_at <> 0 AND token_expires_at < ?`, []any{p0}, `token_expires_at ASC`},
		{1, `token_refreshed_at <> 0 AND token_expires_at >= ? AND token_expires_at < ?`, []any{p0, p1}, `token_expires_at ASC`},
		{2, `token_refreshed_at <> 0 AND token_expires_at >= ?`, []any{p1}, `next_rotate_at ASC`},
		{3, `token_refreshed_at = 0`, nil, `id ASC`},
	}

	var out []RotateTask
	for _, tier := range tiers {
		if tier.prio == 3 && !includeP3 {
			continue
		}
		left := limit - len(out)
		if left <= 0 {
			break
		}
		q := `SELECT ` + rotateTaskCols + ` FROM accounts
		      WHERE disabled = 0 AND status <> 'INVALID'
		        AND next_rotate_at <= ?
		        AND ` + tier.cond
		args := append([]any{nowUnix}, tier.args...)
		if len(suspended) > 0 {
			q += ` AND client_id NOT IN (` + placeholders(len(suspended)) + `)`
			for _, c := range suspended {
				args = append(args, c)
			}
		}
		q += ` ORDER BY ` + tier.order + ` LIMIT ?` + s.forUpdateSkipLocked()
		args = append(args, left)

		rows, err := s.query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			t := RotateTask{Priority: tier.prio}
			if err := rows.Scan(&t.AccountID, &t.Email, &t.ClientID, &t.Tenant,
				&t.RefreshTokenEnc, &t.TokenExpiresAt, &t.ProxyID); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// QueueStats 是调度队列的健康度快照。
type QueueStats struct {
	Backlog    int `json:"backlog"`     // 已到期待处理数
	P0         int `json:"p0"`          // 距硬到期不足 7 天，应恒为 0
	P1         int `json:"p1"`          // 距硬到期不足 15 天
	Unverified int `json:"unverified"`  // 从未轮换
	NeedManual int `json:"need_manual"` // 连续失败 5 次以上
	// Capped 为真表示某一项已经数到上限就停了，真实值只多不少。
	// 界面据此把数字显示成"10 万+"，而不是把一个封顶值当成准确值给人看。
	Capped bool `json:"capped"`
}

// queueCountCap 是队列自检每一项最多数到多少。
//
// 这五个数原来是五条 COUNT(*)。十万账号时是 146 毫秒，看着没问题；
// 但 COUNT(*) 的代价与行数成正比，十亿行线性外推是二十多分钟 ——
// 总览页会直接超时，而调度器每轮都要读它。
//
// 数到十万就停，是因为**再精确也没有用**：
//
//   - 积压驱动的是每分钟配额（积压 ÷ 60），而配额还要被速率上限截断，
//     积压超过上限的六十倍之后，多一个少一个不改变任何决策。
//   - P0 只要判"是不是大于 0"就够触发告警了。
//   - 页面上"积压 10 万+"和"积压 3 亿"给人的行动是同一个。
//
// 有了对应的部分索引，数到十万只是扫十万个索引项，毫秒级，与表有多大无关。
const queueCountCap = 100000

// countUpTo 数出满足条件的行数，最多数到 cap 就停。
//
// 写成子查询套 LIMIT 而不是 COUNT(*)：LIMIT 让扫描在够数时立刻中止，
// 这是"代价与结果规模成正比"而不是"与表规模成正比"的唯一写法，
// 两种数据库都支持。第二个返回值表示是否撞到了上限。
func (s *Store) countUpTo(ctx context.Context, limit int, where string, args ...any) (int, bool, error) {
	return s.countUpToFrom(ctx, limit, "accounts", where, args...)
}

// countUpToAliased 与 countUpTo 相同，但把账号表别名为 a，
// 供 AccountFilter 拼出来的条件使用 —— 那些条件里写的是 a.email、a.id。
func (s *Store) countUpToAliased(ctx context.Context, limit int, where string, args ...any) (int, bool, error) {
	return s.countUpToFrom(ctx, limit, "accounts a", where, args...)
}

func (s *Store) countUpToFrom(ctx context.Context, limit int, from, where string, args ...any) (int, bool, error) {
	var n int
	q := `SELECT COUNT(*) FROM (SELECT 1 FROM ` + from + ` WHERE ` + where + ` LIMIT ?) t`
	qargs := make([]any, 0, len(args)+1)
	qargs = append(append(qargs, args...), limit)
	if err := s.queryRow(ctx, q, qargs...).Scan(&n); err != nil {
		return 0, false, err
	}
	return n, n >= limit, nil
}

// SchedulerStats 统计队列健康度，供总览页与容量自检使用。
//
// 每一项的 WHERE 都必须与 accountIndexes 里对应的部分索引谓词字面一致，
// 否则规划器用不上那个索引，这里就又变回全表扫描 —— 而且不会报错，
// 只会在某天账号涨上去之后突然变慢。改动其中一边时另一边要一起改。
func (s *Store) SchedulerStats(ctx context.Context) (QueueStats, error) {
	now := time.Now()
	var st QueueStats
	const base = `disabled = 0 AND status <> 'INVALID' AND `

	count := func(dst *int, where string, args ...any) error {
		n, capped, err := s.countUpTo(ctx, queueCountCap, base+where, args...)
		if err != nil {
			return err
		}
		*dst = n
		st.Capped = st.Capped || capped
		return nil
	}

	if err := count(&st.Backlog, `next_rotate_at <= ?`, now.Unix()); err != nil {
		return st, err
	}
	if err := count(&st.P0, `token_refreshed_at <> 0 AND token_expires_at < ?`,
		now.Add(7*24*time.Hour).Unix()); err != nil {
		return st, err
	}
	if err := count(&st.P1, `token_refreshed_at <> 0 AND token_expires_at < ?`,
		now.Add(15*24*time.Hour).Unix()); err != nil {
		return st, err
	}
	if err := count(&st.Unverified, `token_refreshed_at = 0`); err != nil {
		return st, err
	}
	if err := count(&st.NeedManual, `rotate_fail_count >= 5`); err != nil {
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
