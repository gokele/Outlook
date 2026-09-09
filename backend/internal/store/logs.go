package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/kele/outlook-console/internal/model"
)

// InsertFetchLog 写一条取件或轮换日志。
// 只记条数与结果，不记主题、发件人与正文，否则等于变相存了邮件。
func (s *Store) InsertFetchLog(ctx context.Context, l *model.FetchLog) error {
	if l.CreatedAt == 0 {
		l.CreatedAt = time.Now().Unix()
	}
	_, err := s.exec(ctx,
		`INSERT INTO fetch_logs (account_id, trigger_src, channel, folder_coverage, token_tier,
		   api_key_id, duration_ms, msg_count, result, error_code, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		l.AccountID, l.Trigger, l.Channel, l.FolderCoverage, l.TokenTier,
		l.APIKeyID, l.DurationMS, l.MsgCount, l.Result, l.ErrorCode, l.CreatedAt)
	return err
}

// LogFilter 是日志查询条件。
type LogFilter struct {
	Type      string // fetch 取件（ui/api）；rotate 轮换（scheduler/manual）；reveal 查看密码
	AccountID *int64
	Result    string
	Page      int
	Size      int
}

// ListFetchLogs 分页查询日志。
func (s *Store) ListFetchLogs(ctx context.Context, f LogFilter) ([]model.FetchLog, int, error) {
	var cond []string
	var args []any
	switch f.Type {
	case "rotate":
		cond = append(cond, "l.trigger_src IN ('scheduler','manual')")
	case "fetch":
		cond = append(cond, "l.trigger_src IN ('ui','api')")
	case "reveal":
		cond = append(cond, "l.trigger_src = 'reveal'")
	}
	if f.AccountID != nil {
		cond = append(cond, "l.account_id = ?")
		args = append(args, *f.AccountID)
	}
	if f.Result != "" {
		cond = append(cond, "l.result = ?")
		args = append(args, f.Result)
	}
	w := ""
	if len(cond) > 0 {
		w = " WHERE " + strings.Join(cond, " AND ")
	}

	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM fetch_logs l`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Size <= 0 {
		f.Size = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	q := `SELECT l.id, l.account_id, COALESCE(a.email,''), l.trigger_src, l.channel, l.folder_coverage,
	        l.token_tier, l.api_key_id, l.duration_ms, l.msg_count, l.result, l.error_code, l.created_at
	      FROM fetch_logs l LEFT JOIN accounts a ON a.id = l.account_id` + w +
		` ORDER BY l.id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Size, (f.Page-1)*f.Size)

	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.FetchLog{}
	for rows.Next() {
		var l model.FetchLog
		var keyID sql.NullInt64
		if err := rows.Scan(&l.ID, &l.AccountID, &l.AccountEmail, &l.Trigger, &l.Channel,
			&l.FolderCoverage, &l.TokenTier, &keyID, &l.DurationMS, &l.MsgCount,
			&l.Result, &l.ErrorCode, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		if keyID.Valid {
			v := keyID.Int64
			l.APIKeyID = &v
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// PurgeOldLogs 清理超过保留期的日志。fetch_logs 是唯一会持续增长的表。
func (s *Store) PurgeOldLogs(ctx context.Context, keepDays int) error {
	if keepDays <= 0 {
		keepDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays).Unix()
	_, err := s.exec(ctx, `DELETE FROM fetch_logs WHERE created_at < ?`, cutoff)
	return err
}

// FetchStats 是近 N 天的取件统计。
type FetchStats struct {
	OK   int `json:"ok"`
	Fail int `json:"fail"`
}

// FetchStatsSince 统计指定时间之后的取件成败数。
func (s *Store) FetchStatsSince(ctx context.Context, since int64) (FetchStats, error) {
	var st FetchStats
	rows, err := s.query(ctx,
		`SELECT result, COUNT(*) FROM fetch_logs
		 WHERE created_at >= ? AND trigger_src IN ('ui','api') GROUP BY result`, since)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		var n int
		if err := rows.Scan(&r, &n); err != nil {
			return st, err
		}
		if r == "ok" {
			st.OK = n
		} else {
			st.Fail += n
		}
	}
	return st, rows.Err()
}

// TokenTierCounts 统计三档取令牌的实际调用量，用于核对对令牌端点的真实请求数。
func (s *Store) TokenTierCounts(ctx context.Context, since int64) (map[string]int, error) {
	rows, err := s.query(ctx,
		`SELECT token_tier, COUNT(*) FROM fetch_logs
		 WHERE created_at >= ? AND token_tier <> '' GROUP BY token_tier`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{"cached": 0, "fetch": 0, "rotate": 0}
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// DeleteFetchLogs 按 id 删除选中的日志，返回删除条数。
func (s *Store) DeleteFetchLogs(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	res, err := s.exec(ctx,
		`DELETE FROM fetch_logs WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClearFetchLogs 清空日志。logType 为 fetch、rotate 或 reveal 时只清对应触发来源，all 或空则全清。
// 返回删除条数。
func (s *Store) ClearFetchLogs(ctx context.Context, logType string) (int64, error) {
	var q string
	var args []any
	switch logType {
	case "fetch":
		q = `DELETE FROM fetch_logs WHERE trigger_src IN ('ui','api')`
	case "rotate":
		q = `DELETE FROM fetch_logs WHERE trigger_src IN ('scheduler','manual')`
	case "reveal":
		q = `DELETE FROM fetch_logs WHERE trigger_src = 'reveal'`
	default:
		q = `DELETE FROM fetch_logs`
	}
	res, err := s.exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
