package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// migrations 是按顺序执行的迁移。绝大多数是一条纯 SQL，全部使用两种数据库
// 共有的语法：时间为 INTEGER 的 Unix 秒，布尔为 INTEGER 的 0/1，JSON 为 TEXT。
//
// 少数几条要按方言分支或要在 Go 里算值（比如分片号是哈希算出来的，SQL 表达不了），
// 用 fn 而不是 sql。两者只能选一个。
var migrations = []migration{
	{name: "001_accounts", sql: `
CREATE TABLE IF NOT EXISTS accounts (
  id                 BIGSERIAL PRIMARY KEY,
  email              TEXT    NOT NULL UNIQUE,
  password_enc       BYTEA,
  client_id          TEXT    NOT NULL,
  refresh_token_enc  BYTEA   NOT NULL,
  tenant             TEXT    NOT NULL DEFAULT 'consumers',
  capabilities       TEXT    NOT NULL DEFAULT '{}',
  channel_policy     TEXT    NOT NULL DEFAULT 'auto',
  category_id        BIGINT,
  note               TEXT    NOT NULL DEFAULT '',
  status             TEXT    NOT NULL DEFAULT 'UNVERIFIED',
  token_refreshed_at BIGINT  NOT NULL DEFAULT 0,
  token_expires_at   BIGINT  NOT NULL DEFAULT 0,
  next_rotate_at     BIGINT  NOT NULL DEFAULT 0,
  rotate_fail_count  INTEGER NOT NULL DEFAULT 0,
  last_fetch_at      BIGINT  NOT NULL DEFAULT 0,
  last_error         TEXT    NOT NULL DEFAULT '',
  disabled           INTEGER NOT NULL DEFAULT 0,
  created_at         BIGINT  NOT NULL DEFAULT 0
)`},
	{name: "002_accounts_idx", sql: `
CREATE INDEX IF NOT EXISTS idx_accounts_rotate ON accounts (next_rotate_at, token_expires_at)
  WHERE disabled = 0 AND status <> 'INVALID'`},
	{name: "003_accounts_idx2", sql: `CREATE INDEX IF NOT EXISTS idx_accounts_category ON accounts (category_id)`},
	{name: "004_accounts_idx3", sql: `CREATE INDEX IF NOT EXISTS idx_accounts_client ON accounts (client_id)`},
	{name: "005_account_tokens", sql: `
CREATE TABLE IF NOT EXISTS account_tokens (
  account_id       BIGINT  NOT NULL,
  scope            TEXT    NOT NULL,
  access_token_enc BYTEA   NOT NULL,
  expires_at       BIGINT  NOT NULL,
  updated_at       BIGINT  NOT NULL,
  PRIMARY KEY (account_id, scope)
)`},
	{name: "006_categories", sql: `
CREATE TABLE IF NOT EXISTS categories (
  id    BIGSERIAL PRIMARY KEY,
  name  TEXT    NOT NULL UNIQUE,
  color TEXT    NOT NULL DEFAULT '#0E6B8E',
  sort  INTEGER NOT NULL DEFAULT 0
)`},
	{name: "007_tags", sql: `
CREATE TABLE IF NOT EXISTS tags (
  id   BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL UNIQUE
)`},
	{name: "008_account_tags", sql: `
CREATE TABLE IF NOT EXISTS account_tags (
  account_id BIGINT NOT NULL,
  tag_id     BIGINT NOT NULL,
  PRIMARY KEY (account_id, tag_id)
)`},
	{name: "009_api_keys", sql: `
CREATE TABLE IF NOT EXISTS api_keys (
  id                   BIGSERIAL PRIMARY KEY,
  name                 TEXT    NOT NULL,
  key_hash             TEXT    NOT NULL UNIQUE,
  prefix               TEXT    NOT NULL,
  scope_category_ids   TEXT    NOT NULL DEFAULT '[]',
  rate_limit_qps       INTEGER NOT NULL DEFAULT 10,
  ip_allowlist         TEXT    NOT NULL DEFAULT '[]',
  allow_export_secrets INTEGER NOT NULL DEFAULT 0,
  allow_lease          INTEGER NOT NULL DEFAULT 0,
  last_used_at         BIGINT  NOT NULL DEFAULT 0,
  revoked_at           BIGINT  NOT NULL DEFAULT 0,
  created_at           BIGINT  NOT NULL DEFAULT 0
)`},
	{name: "010_fetch_logs", sql: `
CREATE TABLE IF NOT EXISTS fetch_logs (
  id              BIGSERIAL PRIMARY KEY,
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
  created_at      BIGINT  NOT NULL DEFAULT 0
)`},
	{name: "011_fetch_logs_idx", sql: `CREATE INDEX IF NOT EXISTS idx_fetch_logs_time ON fetch_logs (created_at DESC)`},
	{name: "012_fetch_logs_idx2", sql: `CREATE INDEX IF NOT EXISTS idx_fetch_logs_acc ON fetch_logs (account_id, created_at DESC)`},
	{name: "013_client_apps", sql: `
CREATE TABLE IF NOT EXISTS client_apps (
  client_id       TEXT PRIMARY KEY,
  name            TEXT   NOT NULL DEFAULT '',
  req_count_1h    INTEGER NOT NULL DEFAULT 0,
  auth_fail_1h    INTEGER NOT NULL DEFAULT 0,
  window_start    BIGINT NOT NULL DEFAULT 0,
  suspended_until BIGINT NOT NULL DEFAULT 0,
  last_alert_at   BIGINT NOT NULL DEFAULT 0
)`},
	{name: "014_account_leases", sql: `
CREATE TABLE IF NOT EXISTS account_leases (
  account_id  BIGINT PRIMARY KEY,
  api_key_id  BIGINT NOT NULL,
  acquired_at BIGINT NOT NULL,
  expires_at  BIGINT NOT NULL
)`},
	{name: "015_users", sql: `
CREATE TABLE IF NOT EXISTS users (
  id            BIGSERIAL PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'admin',
  last_login_at BIGINT NOT NULL DEFAULT 0
)`},
	{name: "016_settings", sql: `
CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value_json TEXT NOT NULL
)`},
	{name: "017_sessions", sql: `
CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  expires_at BIGINT NOT NULL
)`},
	{name: "018_proxy_groups", sql: `
CREATE TABLE IF NOT EXISTS proxy_groups (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT    NOT NULL UNIQUE,
  failover_mode TEXT    NOT NULL DEFAULT 'within_group',
  sticky_return INTEGER NOT NULL DEFAULT 1,
  note          TEXT    NOT NULL DEFAULT '',
  created_at    BIGINT  NOT NULL DEFAULT 0
)`},
	{name: "019_proxies", sql: `
CREATE TABLE IF NOT EXISTS proxies (
  id            BIGSERIAL PRIMARY KEY,
  group_id      BIGINT,
  name          TEXT    NOT NULL DEFAULT '',
  url_enc       BYTEA   NOT NULL,
  weight        INTEGER NOT NULL DEFAULT 1,
  max_accounts  INTEGER NOT NULL DEFAULT 0,
  enabled       INTEGER NOT NULL DEFAULT 1,
  healthy       INTEGER NOT NULL DEFAULT 1,
  last_check_at BIGINT  NOT NULL DEFAULT 0,
  last_error    TEXT    NOT NULL DEFAULT '',
  created_at    BIGINT  NOT NULL DEFAULT 0
)`},
	{name: "020_accounts_proxy", sql: `ALTER TABLE accounts ADD COLUMN proxy_id BIGINT`},
	{name: "021_accounts_proxy_pinned", sql: `ALTER TABLE accounts ADD COLUMN proxy_pinned INTEGER NOT NULL DEFAULT 0`},
	{name: "022_accounts_proxy_fallback", sql: `ALTER TABLE accounts ADD COLUMN proxy_fallback_id BIGINT`},
	{name: "023_categories_proxy_group", sql: `ALTER TABLE categories ADD COLUMN proxy_group_id BIGINT`},
	{name: "024_proxies_idx", sql: `CREATE INDEX IF NOT EXISTS idx_accounts_proxy ON accounts (proxy_id)`},
	{name: "025_sessions_secrets_until", sql: `ALTER TABLE sessions ADD COLUMN secrets_until BIGINT NOT NULL DEFAULT 0`},
	{name: "026_accounts_recovery_email", sql: `ALTER TABLE accounts ADD COLUMN recovery_email TEXT NOT NULL DEFAULT ''`},
	{name: "027_accounts_recovery_password", sql: `ALTER TABLE accounts ADD COLUMN recovery_password_enc BYTEA`},
	{name: "028_accounts_last_error_code", sql: `ALTER TABLE accounts ADD COLUMN last_error_code TEXT NOT NULL DEFAULT ''`},
	{name: "029_account_projects", sql: `
CREATE TABLE IF NOT EXISTS account_projects (
  account_id   BIGINT NOT NULL,
  project_key  TEXT   NOT NULL,
  result       TEXT   NOT NULL,
  completed_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (account_id, project_key)
)`},
	{name: "030_account_projects_idx", sql: `CREATE INDEX IF NOT EXISTS idx_account_projects_key
 ON account_projects (project_key, result)`},
	{name: "031_accounts_cooldown", sql: `ALTER TABLE accounts ADD COLUMN cooldown_until BIGINT NOT NULL DEFAULT 0`},
	{name: "032_fetch_logs_code", sql: `ALTER TABLE fetch_logs ADD COLUMN code_result TEXT NOT NULL DEFAULT ''`},
	// 默认 1：既有的 Key 在升级前就能读正文，升级不该悄悄改变它们的权限。
	{name: "033_api_keys_allow_body", sql: `ALTER TABLE api_keys ADD COLUMN allow_body INTEGER NOT NULL DEFAULT 1`},

	// ---- 账号表水平切分 ----
	//
	// 下面四条把 accounts 从"一张大表"改成"按 shard 切开的一组表"。
	// 顺序不能调换：先补列、再回填、再切分区、最后重建索引 ——
	// 切分区会连同旧表一起丢掉旧索引，所以建索引必须排在最后。

	// 默认 -1 表示"还没算出来"。用哨兵值而不是 0，是为了让回填能识别出
	// 哪些行还没处理过：0 是一个合法分片号，拿它当"未处理"会漏掉真正落在 0 号分片的账号。
	{name: "034_accounts_shard", sql: `ALTER TABLE accounts ADD COLUMN shard SMALLINT NOT NULL DEFAULT -1`},
	{name: "035_accounts_domain", sql: `ALTER TABLE accounts ADD COLUMN domain TEXT NOT NULL DEFAULT ''`},
	{name: "036_backfill_shard_domain", fn: backfillShardDomain},
	{name: "037_partition_accounts", fn: partitionAccounts},
	{name: "038_account_indexes", fn: ensureAccountIndexes},
}

// accountIndexes 是 accounts 上的全部索引，定义只此一处。
//
// 集中写而不是散在各条迁移里，是因为切分区会重建整张表：旧索引随旧表一起消失，
// 必须有一个权威清单把它们照原样建回来。分散定义的后果是切完之后少掉一两个索引，
// 而少索引不会报错，只会让某个查询在某天悄悄变成全表扫描。
//
// 在 PostgreSQL 上这些语句作用于分区父表，会自动在每个分区上建出对应的本地索引。
var accountIndexes = []string{
	// 调度器取任务的主索引。
	`CREATE INDEX IF NOT EXISTS idx_accounts_rotate ON accounts (next_rotate_at, token_expires_at)
	   WHERE disabled = 0 AND status <> 'INVALID'`,
	`CREATE INDEX IF NOT EXISTS idx_accounts_category ON accounts (category_id)`,
	`CREATE INDEX IF NOT EXISTS idx_accounts_client ON accounts (client_id)`,
	`CREATE INDEX IF NOT EXISTS idx_accounts_proxy ON accounts (proxy_id)`,
	// 按域名筛选。有了 domain 列这一维才吃得上索引 ——
	// 原来的 email LIKE '%@outlook.com' 是前缀通配，任何索引都用不上。
	`CREATE INDEX IF NOT EXISTS idx_accounts_domain ON accounts (domain)`,
	// 领取空闲账号：按最久未取件排序。原来这里没有索引，十万账号就要
	// 全表扫描加一次临时排序，是 /mail/latest 路径上最贵的一步。
	`CREATE INDEX IF NOT EXISTS idx_accounts_free ON accounts (last_fetch_at)
	   WHERE disabled = 0 AND status NOT IN ('INVALID','BANNED')`,
	// 分区表的主键是 (shard, id)，它排不了 "WHERE id = ?"。
	// 管理后台几乎每个操作都按 id 找账号，没有这一条会退化成 64 次全分区扫描。
	// SQLite 上 id 本来就是主键，这条索引冗余但无害。
	`CREATE INDEX IF NOT EXISTS idx_accounts_id ON accounts (id)`,
	// 状态筛选与总览页的分状态计数。
	`CREATE INDEX IF NOT EXISTS idx_accounts_status ON accounts (status)`,

	// 下面三条是给队列自检用的。它们都是部分索引，只收录真正要数的那一小撮行，
	// 因此索引本身很小，数起来是"扫几千个索引项"而不是"扫十亿行数据"。
	//
	// 关键在于谓词要和查询里写的一模一样，规划器才认得出可以用它。
	// 所以 SchedulerStats 里的条件是照着这里抄的，两边必须一起改。

	// 按硬到期时间排队：P0/P1 的判定，以及调度器取紧急任务。
	// 谓词里排除未轮换的账号不是可有可无 —— 它们的 token_expires_at 全是 0，
	// 会整整齐齐排在索引最前面，不排除掉的话每次查 P0 都要先趟过它们全部。
	`CREATE INDEX IF NOT EXISTS idx_accounts_expiry ON accounts (token_expires_at)
	   WHERE disabled = 0 AND status <> 'INVALID' AND token_refreshed_at <> 0`,
	// 首验队列。
	`CREATE INDEX IF NOT EXISTS idx_accounts_unverified ON accounts (id)
	   WHERE disabled = 0 AND status <> 'INVALID' AND token_refreshed_at = 0`,
	// 需要人工处理的账号。这一撮永远很少，索引也就永远很小。
	`CREATE INDEX IF NOT EXISTS idx_accounts_needmanual ON accounts (id)
	   WHERE disabled = 0 AND status <> 'INVALID' AND rotate_fail_count >= 5`,
}

// errMigrationDeferred 表示这条迁移这次不做，下次启动再判断。
//
// 与"失败"的区别是它不中断启动：分区转换要整表重写，在一张已经很大的表上
// 做这件事会把服务锁住几十分钟。那种代价必须由人来挑时间承担，
// 而不是某次例行重启时冷不丁发生。
var errMigrationDeferred = errors.New("迁移已推迟")

// maxAutoPartitionRows 是允许在启动时自动切分区的行数上限。
//
// 取一百万：这个量级的整表重写是秒级，重启时顺手做掉没有感觉。
// 再多就该由运维挑一个维护窗口，所以超过这个数只提示、不动手。
//
// 是变量而不是常量，只为让用例能把阈值调下来验证推迟这条路径 ——
// 造一百万行账号来触发它不现实。运行期没有任何地方会改它。
var maxAutoPartitionRows int64 = 1_000_000

// backfillShardDomain 为存量账号补上分片号与域名。
//
// 分片号是 crc32 哈希，SQL 里表达不出来，只能取回邮箱在 Go 里算。分批提交而不是
// 一条大事务：一次性更新百万行会产生一个巨大的事务，锁持有时间长，
// 中途失败还要整段回滚重来。分批之后每一批都是已完成的进度，重启接着做。
func backfillShardDomain(ctx context.Context, s *Store) error {
	const batch = 2000
	started, done := time.Now(), 0
	for {
		rows, err := s.query(ctx,
			`SELECT id, email FROM accounts WHERE shard < 0 ORDER BY id LIMIT ?`, batch)
		if err != nil {
			return err
		}
		type row struct {
			id    int64
			email string
		}
		var buf []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.email); err != nil {
				rows.Close()
				return err
			}
			buf = append(buf, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(buf) == 0 {
			if done > 0 {
				slog.Info("账号分片号回填完成", "rows", done, "took", time.Since(started).String())
			}
			return nil
		}
		err = s.WithTx(ctx, func(tx *Tx) error {
			for _, r := range buf {
				if _, err := tx.exec(ctx, `UPDATE accounts SET shard = ?, domain = ? WHERE id = ?`,
					ShardOf(r.email), DomainOf(r.email), r.id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		done += len(buf)
		if done%100000 == 0 {
			slog.Info("账号分片号回填中", "rows", done)
		}
	}
}

// ensureAccountIndexes 建齐 accounts 上的全部索引，已存在的跳过。
func ensureAccountIndexes(ctx context.Context, s *Store) error {
	for _, q := range accountIndexes {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("建索引失败 (%s): %w", firstLine(q), err)
		}
	}
	return nil
}

// firstLine 取语句的第一行，用于错误信息，避免把整段 SQL 打进日志。
func firstLine(q string) string {
	q = strings.TrimSpace(q)
	if i := strings.IndexByte(q, '\n'); i > 0 {
		return q[:i]
	}
	return q
}

// migration 是一条迁移。sql 与 fn 只能有一个。
type migration struct {
	name string
	sql  string
	fn   func(context.Context, *Store) error
}

// Migrate 建表并记录已执行的脚本。脚本以 PostgreSQL 语法书写，
// 再按方言做少量文本替换，避免维护两份 schema。
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("创建迁移记录表失败: %w", err)
	}
	for _, m := range migrations {
		var got string
		err := s.queryRow(ctx, `SELECT name FROM schema_migrations WHERE name = ?`, m.name).Scan(&got)
		if err == nil {
			continue // 已执行
		}
		if m.fn != nil {
			if err := m.fn(ctx, s); err != nil {
				// 推迟不是失败：这条这次不做，后面的照常做，下次启动再判断一次。
				// 理由由 fn 自己写进日志，那里才知道推迟的是什么、条件是什么。
				if errors.Is(err, errMigrationDeferred) {
					continue
				}
				return fmt.Errorf("执行迁移 %s 失败: %w", m.name, err)
			}
		} else if _, err := s.db.ExecContext(ctx, s.adaptDDL(m.sql)); err != nil {
			return fmt.Errorf("执行迁移 %s 失败: %w", m.name, err)
		}
		if _, err := s.exec(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, m.name); err != nil {
			return fmt.Errorf("记录迁移 %s 失败: %w", m.name, err)
		}
	}
	return nil
}

// adaptDDL 把以 PostgreSQL 语法书写的建表脚本改写成目标方言。
// 两种数据库在 DDL 上的差异只有自增主键与二进制类型两处。
func (s *Store) adaptDDL(q string) string {
	if s.dialect != SQLite {
		return q
	}
	r := strings.NewReplacer(
		"BIGSERIAL PRIMARY KEY", "INTEGER PRIMARY KEY AUTOINCREMENT",
		"BYTEA", "BLOB",
	)
	return r.Replace(q)
}
