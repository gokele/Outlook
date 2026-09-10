package store

import (
	"context"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

// RecordClientReq 记录一次针对某 client_id 的令牌请求，并在需要时滚动统计窗口。
// authFail 为真表示这是一次认证类失败，网络与限流类失败不计入。
func (s *Store) RecordClientReq(ctx context.Context, clientID string, authFail bool) error {
	now := time.Now().Unix()
	winStart := now - 3600

	// 窗口已过期则重置计数，否则累加。两种数据库都支持这种条件更新。
	_, err := s.exec(ctx,
		`INSERT INTO client_apps (client_id, req_count_1h, auth_fail_1h, window_start)
		 VALUES (?, 1, ?, ?)
		 ON CONFLICT (client_id) DO UPDATE SET
		   req_count_1h = CASE WHEN client_apps.window_start < ? THEN 1
		                       ELSE client_apps.req_count_1h + 1 END,
		   auth_fail_1h = CASE WHEN client_apps.window_start < ? THEN ?
		                       ELSE client_apps.auth_fail_1h + ? END,
		   window_start = CASE WHEN client_apps.window_start < ? THEN ?
		                       ELSE client_apps.window_start END`,
		clientID, boolInt(authFail), now,
		winStart, winStart, boolInt(authFail), boolInt(authFail), winStart, now)
	return err
}

// GetClientApp 读出某 client_id 的统计与熔断状态。
func (s *Store) GetClientApp(ctx context.Context, clientID string) (*model.ClientApp, error) {
	var c model.ClientApp
	var winStart int64
	err := s.queryRow(ctx,
		`SELECT client_id, name, req_count_1h, auth_fail_1h, window_start, suspended_until, last_alert_at
		 FROM client_apps WHERE client_id = ?`, clientID).
		Scan(&c.ClientID, &c.Name, &c.ReqCount1h, &c.AuthFail1h, &winStart, &c.SuspendedUntil, &c.LastAlertAt)
	if err != nil {
		return nil, err
	}
	// 窗口已过期时对外呈现为零，避免用陈旧计数触发熔断。
	if winStart < time.Now().Unix()-3600 {
		c.ReqCount1h, c.AuthFail1h = 0, 0
	}
	return &c, nil
}

// SuspendClient 熔断某 client_id，暂停该组账号的全部令牌请求。
// 熔断期间不把任何账号标记为失效，避免应用级故障造成大规模误判。
func (s *Store) SuspendClient(ctx context.Context, clientID string, d time.Duration) error {
	until := time.Now().Add(d).Unix()
	_, err := s.exec(ctx,
		`INSERT INTO client_apps (client_id, suspended_until, last_alert_at, window_start)
		 VALUES (?,?,?,?)
		 ON CONFLICT (client_id) DO UPDATE SET
		   suspended_until = excluded.suspended_until,
		   last_alert_at = excluded.last_alert_at`,
		clientID, until, time.Now().Unix(), time.Now().Unix())
	return err
}

// SuspendedClients 返回当前处于熔断中的 client_id 列表，调度器据此排除对应账号。
func (s *Store) SuspendedClients(ctx context.Context) ([]string, error) {
	rows, err := s.query(ctx,
		`SELECT client_id FROM client_apps WHERE suspended_until > ?`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListClientApps 列出全部 client_id 及其账号数与熔断状态，供总览页使用。
func (s *Store) ListClientApps(ctx context.Context) ([]model.ClientApp, error) {
	rows, err := s.query(ctx,
		`SELECT a.client_id, COUNT(a.id),
		        COALESCE(MAX(c.req_count_1h),0), COALESCE(MAX(c.auth_fail_1h),0),
		        COALESCE(MAX(c.suspended_until),0)
		 FROM accounts a LEFT JOIN client_apps c ON c.client_id = a.client_id
		 GROUP BY a.client_id ORDER BY COUNT(a.id) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ClientApp{}
	for rows.Next() {
		var c model.ClientApp
		if err := rows.Scan(&c.ClientID, &c.AccountCount, &c.ReqCount1h, &c.AuthFail1h, &c.SuspendedUntil); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountDistinctClientIDs 返回不同 client_id 的数量，用于调度器的容量自检。
func (s *Store) CountDistinctClientIDs(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx,
		`SELECT COUNT(DISTINCT client_id) FROM accounts WHERE disabled = 0 AND status <> 'INVALID'`).Scan(&n)
	return n, err
}
