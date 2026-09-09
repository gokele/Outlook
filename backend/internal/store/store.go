// Package store 是唯一的持久化层。同一套 SQL 同时跑在 PostgreSQL 与 SQLite 上：
// 时间统一存 Unix 秒，布尔存 0/1，JSON 存 TEXT，日期运算一律在 Go 侧完成。
// 两者的差异只有取任务时的加锁子句，收敛在 forUpdateSkipLocked 一处。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Dialect 标识底层数据库。
type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// Store 持有连接池与方言信息。
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// DB 暴露底层连接池，仅供需要自定义查询的场景使用。
func (s *Store) DB() *sql.DB { return s.db }

// Dialect 返回当前方言。
func (s *Store) Dialect() Dialect { return s.dialect }

// IsPostgres 判断是否运行在 PostgreSQL 上。调度器据此决定加锁方式。
func (s *Store) IsPostgres() bool { return s.dialect == Postgres }

// Open 按连接串打开数据库并完成必要的运行参数设置。
// SQLite 形态仅用于本地开发与测试，生产一律使用 PostgreSQL。
func Open(dsn string) (*Store, error) {
	switch {
	case strings.HasPrefix(dsn, "sqlite://"), strings.HasPrefix(dsn, "sqlite:"):
		path := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite://"), "sqlite:")
		if path == "" {
			path = "./data/app.db"
		}
		if err := ensureDir(path); err != nil {
			return nil, err
		}
		// WAL 让读写不互相阻塞，busy_timeout 避免开发时频繁撞上 SQLITE_BUSY。
		q := "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
			"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
		db, err := sql.Open("sqlite", path+q)
		if err != nil {
			return nil, err
		}
		// SQLite 同一时刻只允许一个写事务，用单连接串行化写入比让它抛错更可控。
		db.SetMaxOpenConns(1)
		if err := db.Ping(); err != nil {
			return nil, err
		}
		return &Store{db: db, dialect: SQLite}, nil

	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(5)
		if err := db.Ping(); err != nil {
			return nil, err
		}
		return &Store{db: db, dialect: Postgres}, nil
	}
	return nil, fmt.Errorf("无法识别的数据库连接串: %s", dsn)
}

// Close 关闭连接池。
func (s *Store) Close() error { return s.db.Close() }

// rebind 把统一书写的 ? 占位符按方言改写。PostgreSQL 需要 $1 $2 形式。
func (s *Store) rebind(q string) string {
	if s.dialect != Postgres {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

// forUpdateSkipLocked 返回取任务查询的加锁子句。
// PostgreSQL 用行锁让多实例自动分工；SQLite 部署为单实例，调度器唯一，无需加锁。
func (s *Store) forUpdateSkipLocked() string {
	if s.dialect == Postgres {
		return " FOR UPDATE SKIP LOCKED"
	}
	return ""
}

func (s *Store) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.rebind(q), args...)
}

func (s *Store) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.rebind(q), args...)
}

func (s *Store) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.rebind(q), args...)
}

// WithTx 在一个事务中执行 fn，出错自动回滚。
func (s *Store) WithTx(ctx context.Context, fn func(tx *Tx) error) error {
	raw, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	tx := &Tx{tx: raw, s: s}
	if err := fn(tx); err != nil {
		_ = raw.Rollback()
		return err
	}
	return raw.Commit()
}

// Tx 是事务句柄，方法集与 Store 中需要事务的部分保持一致。
type Tx struct {
	tx *sql.Tx
	s  *Store
}

func (t *Tx) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, t.s.rebind(q), args...)
}

func (t *Tx) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.s.rebind(q), args...)
}

func (t *Tx) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, t.s.rebind(q), args...)
}

// boolInt 把布尔转成数据库中存储的 0/1。
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// placeholders 生成 n 个逗号分隔的 ? 占位符，用于 IN 子句。
func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
