package store

import (
	"context"
	"fmt"
	"strings"
)

// migrations 是按顺序执行的建表脚本。全部使用两种数据库共有的语法：
// 时间为 INTEGER 的 Unix 秒，布尔为 INTEGER 的 0/1，JSON 为 TEXT。
var migrations = []struct {
	name string
	sql  string
}{
	{"001_accounts", `
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
	{"002_accounts_idx", `
CREATE INDEX IF NOT EXISTS idx_accounts_rotate ON accounts (next_rotate_at, token_expires_at)
  WHERE disabled = 0 AND status <> 'INVALID'`},
	{"003_accounts_idx2", `CREATE INDEX IF NOT EXISTS idx_accounts_category ON accounts (category_id)`},
	{"004_accounts_idx3", `CREATE INDEX IF NOT EXISTS idx_accounts_client ON accounts (client_id)`},
	{"005_account_tokens", `
CREATE TABLE IF NOT EXISTS account_tokens (
  account_id       BIGINT  NOT NULL,
  scope            TEXT    NOT NULL,
  access_token_enc BYTEA   NOT NULL,
  expires_at       BIGINT  NOT NULL,
  updated_at       BIGINT  NOT NULL,
  PRIMARY KEY (account_id, scope)
)`},
	{"006_categories", `
CREATE TABLE IF NOT EXISTS categories (
  id    BIGSERIAL PRIMARY KEY,
  name  TEXT    NOT NULL UNIQUE,
  color TEXT    NOT NULL DEFAULT '#0E6B8E',
  sort  INTEGER NOT NULL DEFAULT 0
)`},
	{"007_tags", `
CREATE TABLE IF NOT EXISTS tags (
  id   BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL UNIQUE
)`},
	{"008_account_tags", `
CREATE TABLE IF NOT EXISTS account_tags (
  account_id BIGINT NOT NULL,
  tag_id     BIGINT NOT NULL,
  PRIMARY KEY (account_id, tag_id)
)`},
	{"009_api_keys", `
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
	{"010_fetch_logs", `
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
	{"011_fetch_logs_idx", `CREATE INDEX IF NOT EXISTS idx_fetch_logs_time ON fetch_logs (created_at DESC)`},
	{"012_fetch_logs_idx2", `CREATE INDEX IF NOT EXISTS idx_fetch_logs_acc ON fetch_logs (account_id, created_at DESC)`},
	{"013_client_apps", `
CREATE TABLE IF NOT EXISTS client_apps (
  client_id       TEXT PRIMARY KEY,
  name            TEXT   NOT NULL DEFAULT '',
  req_count_1h    INTEGER NOT NULL DEFAULT 0,
  auth_fail_1h    INTEGER NOT NULL DEFAULT 0,
  window_start    BIGINT NOT NULL DEFAULT 0,
  suspended_until BIGINT NOT NULL DEFAULT 0,
  last_alert_at   BIGINT NOT NULL DEFAULT 0
)`},
	{"014_account_leases", `
CREATE TABLE IF NOT EXISTS account_leases (
  account_id  BIGINT PRIMARY KEY,
  api_key_id  BIGINT NOT NULL,
  acquired_at BIGINT NOT NULL,
  expires_at  BIGINT NOT NULL
)`},
	{"015_users", `
CREATE TABLE IF NOT EXISTS users (
  id            BIGSERIAL PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'admin',
  last_login_at BIGINT NOT NULL DEFAULT 0
)`},
	{"016_settings", `
CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value_json TEXT NOT NULL
)`},
	{"017_sessions", `
CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  expires_at BIGINT NOT NULL
)`},
	{"018_proxy_groups", `
CREATE TABLE IF NOT EXISTS proxy_groups (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT    NOT NULL UNIQUE,
  failover_mode TEXT    NOT NULL DEFAULT 'within_group',
  sticky_return INTEGER NOT NULL DEFAULT 1,
  note          TEXT    NOT NULL DEFAULT '',
  created_at    BIGINT  NOT NULL DEFAULT 0
)`},
	{"019_proxies", `
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
	{"020_accounts_proxy", `ALTER TABLE accounts ADD COLUMN proxy_id BIGINT`},
	{"021_accounts_proxy_pinned", `ALTER TABLE accounts ADD COLUMN proxy_pinned INTEGER NOT NULL DEFAULT 0`},
	{"022_accounts_proxy_fallback", `ALTER TABLE accounts ADD COLUMN proxy_fallback_id BIGINT`},
	{"023_categories_proxy_group", `ALTER TABLE categories ADD COLUMN proxy_group_id BIGINT`},
	{"024_proxies_idx", `CREATE INDEX IF NOT EXISTS idx_accounts_proxy ON accounts (proxy_id)`},
	{"025_sessions_secrets_until", `ALTER TABLE sessions ADD COLUMN secrets_until BIGINT NOT NULL DEFAULT 0`},
	{"026_accounts_recovery_email", `ALTER TABLE accounts ADD COLUMN recovery_email TEXT NOT NULL DEFAULT ''`},
	{"027_accounts_recovery_password", `ALTER TABLE accounts ADD COLUMN recovery_password_enc BYTEA`},
	{"028_accounts_last_error_code", `ALTER TABLE accounts ADD COLUMN last_error_code TEXT NOT NULL DEFAULT ''`},
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
		stmt := s.adaptDDL(m.sql)
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
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
