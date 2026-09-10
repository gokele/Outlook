package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// accountsPartitionedDDL 是分区父表的定义。
//
// 它是 accounts 在迁移 037 那一刻的完整列清单的复刻 —— 迁移是时间快照，
// 后来的列由后来的 ALTER 加到分区表上，不必回头改这里。
//
// 两处约束值得说明：
//
//   - PRIMARY KEY (shard, id)：PostgreSQL 要求分区表的唯一约束必须包含分区键。
//     id 的全局唯一由序列保证，主键里带上 shard 只是为了满足这条规则。
//     代价是 "WHERE id = ?" 用不上主键，因此另建了 idx_accounts_id。
//   - UNIQUE (shard, email)：这才是关键。shard 是由 email 哈希算出来的，
//     同一个邮箱必然落在同一个分区，所以 (shard, email) 唯一就等价于 email 全局唯一。
//     账号查重是导入的地基，分区不能把它弄丢 —— 这也正是分片键选邮箱而不是 id 的原因。
const accountsPartitionedDDL = `
CREATE TABLE accounts (
  id                    BIGSERIAL,
  email                 TEXT    NOT NULL,
  password_enc          BYTEA,
  client_id             TEXT    NOT NULL,
  refresh_token_enc     BYTEA   NOT NULL,
  tenant                TEXT    NOT NULL DEFAULT 'consumers',
  capabilities          TEXT    NOT NULL DEFAULT '{}',
  channel_policy        TEXT    NOT NULL DEFAULT 'auto',
  category_id           BIGINT,
  note                  TEXT    NOT NULL DEFAULT '',
  status                TEXT    NOT NULL DEFAULT 'UNVERIFIED',
  token_refreshed_at    BIGINT  NOT NULL DEFAULT 0,
  token_expires_at      BIGINT  NOT NULL DEFAULT 0,
  next_rotate_at        BIGINT  NOT NULL DEFAULT 0,
  rotate_fail_count     INTEGER NOT NULL DEFAULT 0,
  last_fetch_at         BIGINT  NOT NULL DEFAULT 0,
  last_error            TEXT    NOT NULL DEFAULT '',
  disabled              INTEGER NOT NULL DEFAULT 0,
  created_at            BIGINT  NOT NULL DEFAULT 0,
  proxy_id              BIGINT,
  proxy_pinned          INTEGER NOT NULL DEFAULT 0,
  proxy_fallback_id     BIGINT,
  recovery_email        TEXT    NOT NULL DEFAULT '',
  recovery_password_enc BYTEA,
  last_error_code       TEXT    NOT NULL DEFAULT '',
  cooldown_until        BIGINT  NOT NULL DEFAULT 0,
  shard                 SMALLINT NOT NULL,
  domain                TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (shard, id),
  UNIQUE (shard, email)
) PARTITION BY LIST (shard)`

// accountsCopyCols 是搬运数据时逐列点名的列清单。
// 不用 SELECT *：那要求两张表的列顺序完全一致，而这个前提一旦被将来的
// 某次 ALTER 打破，数据会安静地串到相邻的列里，不会报错。
const accountsCopyCols = `id, email, password_enc, client_id, refresh_token_enc, tenant,
 capabilities, channel_policy, category_id, note, status, token_refreshed_at,
 token_expires_at, next_rotate_at, rotate_fail_count, last_fetch_at, last_error,
 disabled, created_at, proxy_id, proxy_pinned, proxy_fallback_id,
 recovery_email, recovery_password_enc, last_error_code, cooldown_until, shard, domain`

// partitionAccounts 把 accounts 改成按 shard 切开的 LIST 分区表。
//
// SQLite 不做这件事，也不需要：它是本地开发与小规模部署的形态，没有分区功能，
// 而 shard 列照样写入，两边跑的是同一套 SQL。
//
// 转换是整表重写，因此只在表还小的时候自动执行；大表上会推迟并给出提示，
// 由人挑维护窗口来做。见 maxAutoPartitionRows。
func partitionAccounts(ctx context.Context, s *Store) error {
	if s.dialect != Postgres {
		return nil
	}

	var kind string
	err := s.db.QueryRowContext(ctx,
		`SELECT relkind FROM pg_class WHERE oid = to_regclass('accounts')`).Scan(&kind)
	if err != nil {
		return fmt.Errorf("读取 accounts 表信息失败: %w", err)
	}
	if kind == "p" {
		return nil // 已经是分区表
	}

	var rows int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&rows); err != nil {
		return fmt.Errorf("统计 accounts 行数失败: %w", err)
	}
	if rows > maxAutoPartitionRows {
		slog.Warn("accounts 表已超过自动切分区的阈值，本次跳过。"+
			"切分区要整表重写并全程持有排他锁，请挑一个维护窗口手工执行；"+
			"在此之前系统照常工作，只是仍然是单表。",
			"rows", rows, "threshold", maxAutoPartitionRows, "shards", ShardCount)
		return errMigrationDeferred
	}

	var seq sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT pg_get_serial_sequence('accounts', 'id')`).Scan(&seq); err != nil {
		return fmt.Errorf("读取 accounts.id 序列失败: %w", err)
	}

	started := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	run := func(q string) error {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("切分区失败 (%s): %w", firstLine(q), err)
		}
		return nil
	}

	// 旧表让路。旧索引跟着旧表走，最后随 DROP 一起消失，
	// 因此新表的索引由 ensureAccountIndexes 在下一条迁移里统一建回来。
	if err := run(`ALTER TABLE accounts RENAME TO accounts_old`); err != nil {
		return err
	}
	// 序列名不随表名改变，不挪开的话新表的 BIGSERIAL 会撞上同名序列。
	if seq.Valid && seq.String != "" {
		if err := run(`ALTER SEQUENCE ` + seq.String + ` RENAME TO accounts_old_id_seq`); err != nil {
			return err
		}
	}
	if err := run(accountsPartitionedDDL); err != nil {
		return err
	}
	for i := 0; i < ShardCount; i++ {
		q := fmt.Sprintf(`CREATE TABLE accounts_s%02d PARTITION OF accounts FOR VALUES IN (%d)`, i, i)
		if err := run(q); err != nil {
			return err
		}
	}
	if err := run(`INSERT INTO accounts (` + accountsCopyCols + `)
	               SELECT ` + accountsCopyCols + ` FROM accounts_old`); err != nil {
		return err
	}
	if err := run(`DROP TABLE accounts_old`); err != nil {
		return err
	}

	// 序列要接着旧表的最大 id 往下发，否则新账号会拿到已经用过的 id。
	var newSeq string
	if err := tx.QueryRowContext(ctx,
		`SELECT pg_get_serial_sequence('accounts', 'id')`).Scan(&newSeq); err != nil {
		return fmt.Errorf("读取新序列失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`SELECT setval($1, GREATEST((SELECT COALESCE(MAX(id), 0) FROM accounts), 1))`, newSeq); err != nil {
		return fmt.Errorf("重置 accounts.id 序列失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交切分区失败: %w", err)
	}
	slog.Info("accounts 已切成分区表",
		"shards", ShardCount, "rows", rows, "took", time.Since(started).String())
	return nil
}

// IsPartitioned 返回 accounts 是否已经是分区表，供自检与总览页展示。
func (s *Store) IsPartitioned(ctx context.Context) bool {
	if s.dialect != Postgres {
		return false
	}
	var kind string
	err := s.db.QueryRowContext(ctx,
		`SELECT relkind FROM pg_class WHERE oid = to_regclass('accounts')`).Scan(&kind)
	return err == nil && kind == "p"
}
