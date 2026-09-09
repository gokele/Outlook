package store

import (
	"context"
	"strings"
	"time"
)

// 项目维度的复用记录。
//
// 解决的是批量注册里最实际的一个约束：**同一个邮箱在 A 站注册过就不能再注册
// A 站，但注册 B 站完全没问题。** 系统原来只有租约（占用/释放）这一个维度，
// 调用方只好自己在外面记一本账 —— 记错一次就是一批注册失败，而失败要到
// 对方站点报"邮箱已存在"才看得出来。
//
// 只记成功，不记失败：失败的原因五花八门（验证码没收到、对方站点抽风、
// 中途放弃），下次换个时间重试完全合理。只有"确实注册成功了"才构成
// "这个邮箱在这个项目上已经用掉了"。

// ProjectResult 是一次使用的结局。
type ProjectResult string

const (
	// ProjectSuccess 表示该账号在该项目上已经用掉，不应再被领取。
	ProjectSuccess ProjectResult = "success"
	// ProjectFail 表示这次没成，账号可以再被领取。
	ProjectFail ProjectResult = "fail"
)

// NormalizeProjectKey 归一化项目标识。
//
// 去空白并转小写：调用方常常在不同地方写成 "SiteA"、"sitea"、" siteA "，
// 若按字面区分，同一个项目会被当成三个，隔离就失效了 —— 而这种失效是静默的。
func NormalizeProjectKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// RecordProjectUse 记下某账号在某项目上的结局。
//
// 成功写入记录；失败则把已有记录删掉 —— 允许这个账号之后再被同一项目领取。
// 用 upsert 而不是 insert：同一个账号在同一项目上可能被重试多次。
func (s *Store) RecordProjectUse(ctx context.Context, accountID int64,
	projectKey string, result ProjectResult) error {

	key := NormalizeProjectKey(projectKey)
	if key == "" {
		return nil // 没给项目标识就没有隔离可言，静默跳过
	}
	if result != ProjectSuccess {
		_, err := s.exec(ctx,
			`DELETE FROM account_projects WHERE account_id = ? AND project_key = ?`,
			accountID, key)
		return err
	}
	_, err := s.exec(ctx,
		`INSERT INTO account_projects (account_id, project_key, result, completed_at)
		 VALUES (?,?,?,?)
		 ON CONFLICT (account_id, project_key) DO UPDATE SET
		   result = excluded.result, completed_at = excluded.completed_at`,
		accountID, key, string(ProjectSuccess), time.Now().Unix())
	return err
}

// SetCooldown 让账号在一段时间内不被领取。
//
// 失败之后立刻把账号放回池子，下一个调用方多半会立刻拿到同一个 ——
// 它按 last_fetch_at 升序挑，刚用过的反而排在最前。而刚刚失败的账号
// 大概率会接着失败，那就成了一个卡住整个池子的循环。
func (s *Store) SetCooldown(ctx context.Context, accountID int64, d time.Duration) error {
	until := time.Now().Add(d).Unix()
	_, err := s.exec(ctx,
		`UPDATE accounts SET cooldown_until = ? WHERE id = ?`, until, accountID)
	return err
}

// ProjectUsedCount 统计某项目已经用掉多少个账号，供调用方估算余量。
func (s *Store) ProjectUsedCount(ctx context.Context, projectKey string) (int, error) {
	key := NormalizeProjectKey(projectKey)
	if key == "" {
		return 0, nil
	}
	var n int
	err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM account_projects WHERE project_key = ? AND result = ?`,
		key, string(ProjectSuccess)).Scan(&n)
	return n, err
}
