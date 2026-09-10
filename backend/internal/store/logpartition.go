package store

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// 日志表的切分。
//
// accounts 是"存量大"，fetch_logs 是"增量大"，两者压垮数据库的方式并不一样。
// 十亿账号每天要轮换一千六百万次，每次一条日志；再加上取件日志，
// 保留三十天就是五亿行。真正致命的不是它占多少空间，而是**怎么删**：
// `DELETE FROM fetch_logs WHERE created_at < ?` 一次删掉一千六百万行，
// 留下同样多的死元组等着 autovacuum 回收，而它一天也追不完一天的量。
// 删得越多，表越肿，下一次删得越慢。
//
// 所以日志按天分区，过期直接 DROP 掉整个分区 —— 一条元数据操作，
// 瞬间完成，不产生任何死元组。
//
// 但日志里混着两类东西，保留期差一个数量级：
//
//   - ops：取件与轮换日志，机器产生，量大，过一个月就没人再看。
//   - audit：谁查看了哪个账号的凭据，人产生，量极小，要留一年 ——
//     事后追溯往往发生在事情过去很久之后。
//
// 用一个保留期清掉它们，要么审计记录在最需要的时候没有了，要么运营日志
// 一年都删不掉。因此表先按 kind 分成两支，再只对 ops 那一支按天切开：
//
//	fetch_logs                       (LIST kind)
//	├─ fetch_logs_audit              审计，普通表，按时间 DELETE，量小无所谓
//	└─ fetch_logs_ops                (RANGE created_at)
//	   ├─ fetch_logs_ops_20260910    一天一个，过期整个 DROP
//	   └─ fetch_logs_ops_default     兜底，正常应当一直是空的
//
// 分成两层而不是拆成两张表，是为了让 fetch_logs 仍然是一张表：
// 日志页的筛选、统计、清空全都不必改一个字。

// logKindOps 与 logKindAudit 是日志的两支，也是第一层分区键。
const (
	logKindOps   = "ops"
	logKindAudit = "audit"
)

// logPartitionAhead 是提前建出多少天的分区。
//
// 建到后天不是保守，是必须：分区不存在时插入会直接失败。日常维护每天跑，
// 留三天余量意味着维护连着两天没跑上也不会丢日志。
const logPartitionAhead = 3

// fetchLogsPartitionedDDL 是分区父表的定义，与迁移 043 那一刻的列清单一致。
//
// 主键必须包含两级分区键，因此是 (kind, created_at, id)。
// id 的唯一性仍由序列保证，按 id 删日志靠单独的索引。
const fetchLogsPartitionedDDL = `
CREATE TABLE fetch_logs (
  id              BIGSERIAL,
  account_id      BIGINT  NOT NULL DEFAULT 0,
  trigger_src     TEXT    NOT NULL,
  channel         TEXT    NOT NULL DEFAULT '',
  folder_coverage TEXT    NOT NULL DEFAULT '',
  token_tier      TEXT    NOT NULL DEFAULT '',
  api_key_id      BIGINT,
  duration_ms     BIGINT  NOT NULL DEFAULT 0,
  msg_count       INTEGER NOT NULL DEFAULT 0,
  result          TEXT    NOT NULL DEFAULT 'ok',
  error_code      TEXT    NOT NULL DEFAULT '',
  created_at      BIGINT  NOT NULL DEFAULT 0,
  code_result     TEXT    NOT NULL DEFAULT '',
  kind            TEXT    NOT NULL DEFAULT 'ops',
  PRIMARY KEY (kind, created_at, id)
) PARTITION BY LIST (kind)`

const fetchLogsCopyCols = `id, account_id, trigger_src, channel, folder_coverage, token_tier,
 api_key_id, duration_ms, msg_count, result, error_code, created_at, code_result, kind`

// fetchLogsCopySelect 与 fetchLogsCopyCols 一一对应，只把 kind 换成现算。
//
// 存量日志的 kind 在这里算，而不是靠一条单独的 UPDATE 回填：trigger_src 上没有索引，
// 那条 UPDATE 是全表扫描加整表更新，在大表上就是启动时凭空多出来的几分钟。
// 而搬运本来就要把每一行读一遍，顺手算出来不多花一分钱。
const fetchLogsCopySelect = `id, account_id, trigger_src, channel, folder_coverage, token_tier,
 api_key_id, duration_ms, msg_count, result, error_code, created_at, code_result,
 CASE WHEN trigger_src = 'reveal' THEN '` + logKindAudit + `' ELSE '` + logKindOps + `' END`

// logIndexes 是 fetch_logs 上的全部索引，理由同 accountIndexes：
// 切分区会重建整张表，必须有一个权威清单把索引原样建回来。
var logIndexes = []string{
	`CREATE INDEX IF NOT EXISTS idx_fetch_logs_time ON fetch_logs (created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_fetch_logs_acc ON fetch_logs (account_id, created_at DESC)`,
	// 按 id 删单条日志用。分区表的主键排不了单独的 id。
	`CREATE INDEX IF NOT EXISTS idx_fetch_logs_id ON fetch_logs (id)`,
}

// dayStart 返回某个时刻所在自然日（UTC）的起点。
//
// 用 UTC 而不是本地时区：分区边界一旦定下就写进了表定义，
// 而服务器时区是可以被改的 —— 改一次就会出现边界对不上的空档。
func dayStart(t time.Time) int64 {
	return t.UTC().Truncate(24 * time.Hour).Unix()
}

// logPartitionName 返回某一天的分区名。
func logPartitionName(start int64) string {
	return "fetch_logs_ops_" + time.Unix(start, 0).UTC().Format("20060102")
}

// partitionFetchLogs 把 fetch_logs 改成两层分区表。
// 与 partitionAccounts 同样只在表还小的时候自动做，理由见 maxAutoPartitionRows。
func partitionFetchLogs(ctx context.Context, s *Store) error {
	if s.dialect != Postgres {
		return nil
	}

	var kind string
	if err := s.db.QueryRowContext(ctx,
		`SELECT relkind FROM pg_class WHERE oid = to_regclass('fetch_logs')`).Scan(&kind); err != nil {
		return fmt.Errorf("读取 fetch_logs 表信息失败: %w", err)
	}
	if kind == "p" {
		return nil
	}

	var rows int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fetch_logs`).Scan(&rows); err != nil {
		return fmt.Errorf("统计 fetch_logs 行数失败: %w", err)
	}
	if rows > maxAutoPartitionRows {
		slog.Warn("fetch_logs 已超过自动切分区的阈值，本次跳过。"+
			"日志表切分区同样要整表重写，请挑一个维护窗口手工执行；"+
			"在此之前日志照常记录与清理，只是清理仍然走 DELETE。",
			"rows", rows, "threshold", maxAutoPartitionRows)
		return errMigrationDeferred
	}

	// 存量日志实际落在哪些天，就建哪些天的分区。
	//
	// 按 [最早, 最晚] 的跨度一天不落地全建出来会简单些，但那是个陷阱：
	// 一个跑了两年的库会因此凭空多出七百多个空分区，而其中绝大多数
	// 在第一次清理时就会被删掉。取实际存在的日期，多扫一遍表也值得 ——
	// 反正搬运本来就要把整张表读一遍。
	days, err := s.logDaysPresent(ctx)
	if err != nil {
		return err
	}

	var seq string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(pg_get_serial_sequence('fetch_logs', 'id'), '')`).Scan(&seq); err != nil {
		return fmt.Errorf("读取 fetch_logs.id 序列失败: %w", err)
	}

	started := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	run := func(q string) error {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("日志切分区失败 (%s): %w", firstLine(q), err)
		}
		return nil
	}

	if err := run(`ALTER TABLE fetch_logs RENAME TO fetch_logs_old`); err != nil {
		return err
	}
	if seq != "" {
		if err := run(`ALTER SEQUENCE ` + seq + ` RENAME TO fetch_logs_old_id_seq`); err != nil {
			return err
		}
	}
	if err := run(fetchLogsPartitionedDDL); err != nil {
		return err
	}
	if err := run(`CREATE TABLE fetch_logs_audit PARTITION OF fetch_logs FOR VALUES IN ('` + logKindAudit + `')`); err != nil {
		return err
	}
	if err := run(`CREATE TABLE fetch_logs_ops PARTITION OF fetch_logs FOR VALUES IN ('` + logKindOps + `')
	               PARTITION BY RANGE (created_at)`); err != nil {
		return err
	}
	// 兜底分区。正常情况下它一直是空的 —— 有它是因为"分区不存在就插不进去"
	// 这个失败模式太糟：会让取件日志整段丢失，而且是安静地丢。
	if err := run(`CREATE TABLE fetch_logs_ops_default PARTITION OF fetch_logs_ops DEFAULT`); err != nil {
		return err
	}
	for _, q := range logPartitionDDL(days) {
		if err := run(q); err != nil {
			return err
		}
	}
	if err := run(`INSERT INTO fetch_logs (` + fetchLogsCopyCols + `)
	               SELECT ` + fetchLogsCopySelect + ` FROM fetch_logs_old`); err != nil {
		return err
	}

	// 与 accounts 同理：搬完当场数，对不上就整个回滚，旧表原封不动。
	var copied int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM fetch_logs`).Scan(&copied); err != nil {
		return fmt.Errorf("校验日志搬运结果失败: %w", err)
	}
	if copied != rows {
		return fmt.Errorf("日志切分区中止：原表 %d 行，搬过去只有 %d 行，已回滚，原表未改动", rows, copied)
	}

	if err := run(`DROP TABLE fetch_logs_old`); err != nil {
		return err
	}

	var newSeq string
	if err := tx.QueryRowContext(ctx,
		`SELECT pg_get_serial_sequence('fetch_logs', 'id')`).Scan(&newSeq); err != nil {
		return fmt.Errorf("读取新序列失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`SELECT setval($1, GREATEST((SELECT COALESCE(MAX(id), 0) FROM fetch_logs), 1))`, newSeq); err != nil {
		return fmt.Errorf("重置 fetch_logs.id 序列失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交日志切分区失败: %w", err)
	}
	slog.Info("fetch_logs 已切成分区表", "rows", rows, "took", time.Since(started).String())
	return nil
}

// logPartitionDDL 生成按天分区的建表语句：今天与随后几天，外加 extra 里点名的那些天。
//
// extra 传的是存量日志实际落在哪些天（切分区时用），日常维护传空即可。
func logPartitionDDL(extra []int64) []string {
	const day = int64(24 * 3600)
	want := map[int64]bool{}
	now := time.Now()
	for i := 0; i <= logPartitionAhead; i++ {
		want[dayStart(now.AddDate(0, 0, i))] = true
	}
	for _, start := range extra {
		want[start] = true
	}

	starts := make([]int64, 0, len(want))
	for start := range want {
		starts = append(starts, start)
	}
	slices.Sort(starts)

	out := make([]string, 0, len(starts))
	for _, start := range starts {
		out = append(out, fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF fetch_logs_ops FOR VALUES FROM (%d) TO (%d)`,
			logPartitionName(start), start, start+day))
	}
	return out
}

// logDaysPresent 列出存量运营日志实际落在哪些自然日上。
func (s *Store) logDaysPresent(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT (created_at / 86400) * 86400
		 FROM fetch_logs WHERE trigger_src <> 'reveal'`)
	if err != nil {
		return nil, fmt.Errorf("读取日志日期失败: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var start int64
		if err := rows.Scan(&start); err != nil {
			return nil, err
		}
		out = append(out, start)
	}
	return out, rows.Err()
}

// EnsureLogPartitions 建出今天与随后几天的日志分区。
//
// 必须在写日志之前调用，而且要天天调用：分区不存在时插入会失败。
// 兜底分区能接住漏网的行，但那是安全网，不是正常路径 ——
// 落进兜底分区的行会阻碍之后创建覆盖同一区间的分区。
func (s *Store) EnsureLogPartitions(ctx context.Context) error {
	if s.dialect != Postgres || !s.isPartitionedTable(ctx, "fetch_logs") {
		return nil
	}
	for _, q := range logPartitionDDL(nil) {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("创建日志分区失败: %w", err)
		}
	}
	// 兜底分区里有东西，说明某天的维护没跑上。它不影响记录日志，
	// 但会挡住后续建同一天的分区，必须让人知道。
	var stray int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fetch_logs_ops_default`).Scan(&stray); err == nil && stray > 0 {
		slog.Warn("有日志落进了兜底分区，说明当天的分区维护没有跑上",
			"rows", stray, "table", "fetch_logs_ops_default")
	}
	return nil
}

// dropExpiredLogPartitions 删掉整段都已过期的日志分区。
//
// 返回是否走了分区路径：为假时调用方要退回 DELETE。
//
// 只在分区的整个区间都早于 cutoff 时才删，因此实际保留期只会多于设定值，
// 不会少 —— 宁可多留一天，也不能把还在保留期内的日志一起丢掉。
func (s *Store) dropExpiredLogPartitions(ctx context.Context, cutoff int64) (bool, error) {
	if s.dialect != Postgres || !s.isPartitionedTable(ctx, "fetch_logs") {
		return false, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_inherits i ON i.inhrelid = c.oid
		WHERE i.inhparent = to_regclass('fetch_logs_ops')
		  AND c.relname <> 'fetch_logs_ops_default'`)
	if err != nil {
		return false, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return false, err
		}
		names = append(names, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}

	const day = int64(24 * 3600)
	for _, name := range names {
		start, ok := logPartitionStart(name)
		if !ok || start+day > cutoff {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS `+name); err != nil {
			return true, fmt.Errorf("删除过期日志分区 %s 失败: %w", name, err)
		}
		slog.Info("已删除过期日志分区", "partition", name)
	}

	// 兜底分区收的是"日期上没有对应分区"的行 —— 比如补写的历史日志，
	// 或者维护漏跑那天的记录。它不属于任何一天，DROP 不掉，只能按行删。
	// 正常情况下这张表是空的，这条 DELETE 也就不花什么代价；
	// 但没有它，落进兜底分区的行会永远留着，成为一个只增不减的角落。
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM fetch_logs_ops_default WHERE created_at < $1`, cutoff); err != nil {
		return true, fmt.Errorf("清理兜底分区失败: %w", err)
	}
	return true, nil
}

// logPartitionStart 从分区名反解出它覆盖的那一天的起点。
func logPartitionStart(name string) (int64, bool) {
	const prefix = "fetch_logs_ops_"
	if len(name) != len(prefix)+8 || name[:len(prefix)] != prefix {
		return 0, false
	}
	t, err := time.ParseInLocation("20060102", name[len(prefix):], time.UTC)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

// isPartitionedTable 判断某张表是不是分区表。
func (s *Store) isPartitionedTable(ctx context.Context, name string) bool {
	if s.dialect != Postgres {
		return false
	}
	var kind string
	err := s.db.QueryRowContext(ctx,
		`SELECT relkind FROM pg_class WHERE oid = to_regclass($1)`, name).Scan(&kind)
	return err == nil && kind == "p"
}

// ensureLogIndexes 建齐 fetch_logs 上的全部索引，已存在的跳过。
func ensureLogIndexes(ctx context.Context, s *Store) error {
	for _, q := range logIndexes {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("建日志索引失败 (%s): %w", firstLine(q), err)
		}
	}
	return nil
}
