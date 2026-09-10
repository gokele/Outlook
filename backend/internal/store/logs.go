package store

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/gokele/Outlook/internal/model"
)

// InsertFetchLog 写一条取件或轮换日志。
// 只记条数与结果，不记主题、发件人与正文，否则等于变相存了邮件。
func (s *Store) InsertFetchLog(ctx context.Context, l *model.FetchLog) error {
	if l.CreatedAt == 0 {
		l.CreatedAt = time.Now().Unix()
	}
	// kind 决定这条日志落进哪一支分区，也就决定它按哪个保留期清理。
	// 审计与运营日志的保留期差一个数量级，理由见 AuditKeepDays。
	kind := logKindOps
	if l.Trigger == model.TriggerReveal {
		kind = logKindAudit
	}
	if _, err := s.exec(ctx,
		`INSERT INTO fetch_logs (account_id, trigger_src, channel, folder_coverage, token_tier,
		   api_key_id, duration_ms, msg_count, result, error_code, created_at, code_result, kind)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		l.AccountID, l.Trigger, l.Channel, l.FolderCoverage, l.TokenTier,
		l.APIKeyID, l.DurationMS, l.MsgCount, l.Result, l.ErrorCode, l.CreatedAt, l.CodeResult,
		kind); err != nil {
		return err
	}
	// 顺手累加日汇总。写失败不影响这次调用 —— 日志已经落库，
	// 统计是给人看的，不该反过来让主流程失败。
	if err := s.bumpLogStats(ctx, string(l.Trigger), l.Result, l.CodeResult, l.TokenTier, l.CreatedAt); err != nil {
		slog.Warn("累加日志汇总失败", "err", err)
	}
	return nil
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

	// 总数封顶，理由与账号列表一样：日志表是全库增长最快的表，
	// 数准它是一次全表扫描，而分页只需要知道还有没有下一页。
	countWhere := "1=1"
	if w != "" {
		countWhere = strings.TrimPrefix(w, " WHERE ")
	}
	total, _, err := s.countUpToFrom(ctx, MaxListTotal, "fetch_logs l", countWhere, args...)
	if err != nil {
		return nil, 0, err
	}
	if f.Size <= 0 {
		f.Size = 50
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	if max := (MaxListTotal / f.Size) + 1; f.Page > max {
		f.Page = max
	}
	// 按时间倒序而不是按 id 倒序。两者的顺序几乎总是一致（id 来自同一个序列），
	// 但时间是分区键：按它排序能让数据库只翻最近的几个分区，
	// 按 id 排序则要把每个分区都打开比一比。
	q := `SELECT l.id, l.account_id, COALESCE(a.email,''), l.trigger_src, l.channel, l.folder_coverage,
	        l.token_tier, l.api_key_id, l.duration_ms, l.msg_count, l.result, l.error_code,
	        l.created_at, l.code_result
	      FROM fetch_logs l LEFT JOIN accounts a ON a.id = l.account_id` + w +
		` ORDER BY l.created_at DESC, l.id DESC LIMIT ? OFFSET ?`
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
			&l.Result, &l.ErrorCode, &l.CreatedAt, &l.CodeResult); err != nil {
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

// AuditKeepDays 是审计日志的保留期，明显长于普通取件日志。
//
// 「谁在什么时候看了哪个账号的密码」与「某次取件成功没有」是两类东西：
// 后者过了一个月就没人再看，前者恰恰是事后追溯才需要 —— 而事后追溯往往
// 发生在事情过去很久之后。用同一个保留期清掉它，等于在最需要的时候没有记录。
const AuditKeepDays = 365

// PurgeOldLogs 清理超过保留期的日志，并为随后几天备好分区。
//
// 审计日志（trigger_src = reveal）单独用更长的保留期，理由见 AuditKeepDays。
//
// 运营日志在 PostgreSQL 上按天分区，过期是整个分区 DROP 掉 —— 一条元数据操作，
// 不产生任何死元组。这一点在十亿规模下是决定性的：一天一千六百万条日志，
// 用 DELETE 清理会留下同样多的死元组等着 autovacuum 回收，而它一天追不完一天的量，
// 删得越多表越肿，下一次删得越慢。SQLite 上没有分区，仍走 DELETE ——
// 它是小规模形态，那点量删得动。
func (s *Store) PurgeOldLogs(ctx context.Context, keepDays int) error {
	if keepDays <= 0 {
		keepDays = 30
	}
	// 先把未来几天的分区建出来，再清理。顺序不能反：清理会失败，
	// 而分区没建出来会让之后的日志整段写不进去，后果重得多。
	if err := s.EnsureLogPartitions(ctx); err != nil {
		return err
	}

	cutoff := time.Now().AddDate(0, 0, -keepDays).Unix()
	dropped, err := s.dropExpiredLogPartitions(ctx, cutoff)
	if err != nil {
		return err
	}
	if !dropped {
		if _, err := s.exec(ctx,
			`DELETE FROM fetch_logs WHERE created_at < ? AND trigger_src <> ?`,
			cutoff, model.TriggerReveal); err != nil {
			return err
		}
	}

	// 审计日志量极小（一次人工查看凭据一条），不值得为它单独切分区，DELETE 足够。
	auditCutoff := time.Now().AddDate(0, 0, -AuditKeepDays).Unix()
	if _, err := s.exec(ctx,
		`DELETE FROM fetch_logs WHERE created_at < ? AND trigger_src = ?`,
		auditCutoff, model.TriggerReveal); err != nil {
		return err
	}
	return s.purgeLogStats(ctx)
}

// FetchStats 是近 N 天的取件统计。
type FetchStats struct {
	OK   int `json:"ok"`
	Fail int `json:"fail"`
}

// FetchStatsSince 统计指定时间之后的取件成败数。
//
// 读日汇总而不是扫日志：按时间窗口聚合没有索引解法，结果本身就要求把窗口里的
// 每一行都过一遍。十亿规模下 7 天是一亿多行，而这个数字要在每次打开总览页时算出来。
func (s *Store) FetchStatsSince(ctx context.Context, since int64) (FetchStats, error) {
	m, err := s.sumLogStats(ctx, metricFetchResult, since)
	if err != nil {
		return FetchStats{}, err
	}
	return FetchStats{OK: m["ok"], Fail: m["fail"]}, nil
}

// CodeStats 是验证码提取的成败统计。
type CodeStats struct {
	Hit  int `json:"hit"`
	Miss int `json:"miss"`
}

// CodeStatsSince 统计指定时间之后的验证码提取成败。
//
// 只统计确实要求了提取的那些请求（code_result 非空）。没要求提取的取件
// 不该拉低成功率 —— 它本来就不打算提码。
func (s *Store) CodeStatsSince(ctx context.Context, since int64) (CodeStats, error) {
	m, err := s.sumLogStats(ctx, metricCodeResult, since)
	if err != nil {
		return CodeStats{}, err
	}
	return CodeStats{Hit: m["hit"], Miss: m["miss"]}, nil
}

// TokenTierCounts 统计三档取令牌的实际调用量，用于核对对令牌端点的真实请求数。
func (s *Store) TokenTierCounts(ctx context.Context, since int64) (map[string]int, error) {
	m, err := s.sumLogStats(ctx, metricTokenTier, since)
	if err != nil {
		return nil, err
	}
	// 三档都要有键，没出现过的给 0：缺键会让前端把它显示成空白，
	// 而"这一档一次都没走过"本身就是要看的信息。
	out := map[string]int{"cached": 0, "fetch": 0, "rotate": 0}
	for k, v := range m {
		out[k] = v
	}
	return out, nil
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
// 汇总表要跟着一起清，否则日志清空了、总览页的统计还在，
// 界面自己跟自己打架。
//
// 三档取令牌那一维是个例外：它由取件与轮换共同写入，只清掉其中一类日志时
// 分不出该扣掉多少，而重算一遍正是这次要消灭的那种全表聚合。
// 因此它只在整体清空时才归零 —— 与其算个错数，不如说清楚它什么时候动。
func (s *Store) ClearFetchLogs(ctx context.Context, logType string) (int64, error) {
	var q string
	var clearMetrics []string
	switch logType {
	case "fetch":
		q = `DELETE FROM fetch_logs WHERE trigger_src IN ('ui','api')`
		clearMetrics = []string{metricFetchResult, metricCodeResult}
	case "rotate":
		q = `DELETE FROM fetch_logs WHERE trigger_src IN ('scheduler','manual')`
	case "reveal":
		q = `DELETE FROM fetch_logs WHERE trigger_src = 'reveal'`
	default:
		q = `DELETE FROM fetch_logs`
		clearMetrics = []string{metricFetchResult, metricCodeResult, metricTokenTier}
	}
	res, err := s.exec(ctx, q)
	if err != nil {
		return 0, err
	}
	for _, m := range clearMetrics {
		if _, err := s.exec(ctx, `DELETE FROM log_daily_stats WHERE metric = ?`, m); err != nil {
			return 0, err
		}
	}
	return res.RowsAffected()
}
