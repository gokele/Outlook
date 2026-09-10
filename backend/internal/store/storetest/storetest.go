// Package storetest 提供测试用的库连接。
//
// 开发用 SQLite、生产用 PostgreSQL，两者在并发与锁的语义上并不等价：
// 取任务的加锁子句 (FOR UPDATE SKIP LOCKED) 在 SQLite 上根本不存在，
// 占位符也要改写。只跑 SQLite 的测试通过并不能说明生产没问题。
//
// 因此这里让同一套用例既能跑 SQLite，也能通过环境变量切到 PostgreSQL：
//
//	go test ./...                                     # 默认 SQLite
//	TEST_DATABASE_URL=postgres://... go test ./...     # 跑真实 PostgreSQL
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gokele/Outlook/internal/store"
)

// EnvKey 是切换测试库的环境变量名。
const EnvKey = "TEST_DATABASE_URL"

// New 打开一个已完成迁移的测试库，并在用例结束时清理。
//
// SQLite 走临时文件，跑完即弃。PostgreSQL 则为每个用例建一个独立 schema
// 并把 search_path 指过去，结束时整个 schema 删掉 —— 这样用例之间零干扰，
// 也不必依赖清表顺序，还能并行跑。
func New(t *testing.T, name string) *store.Store {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv(EnvKey))
	if dsn == "" {
		return openSQLite(t, name)
	}
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		t.Fatalf("%s 目前只支持 PostgreSQL 连接串，得到 %q", EnvKey, dsn)
	}
	return openPostgres(t, dsn)
}

func openSQLite(t *testing.T, name string) *store.Store {
	t.Helper()
	st, err := store.Open("sqlite://" + filepath.Join(t.TempDir(), name+".db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return st
}

func openPostgres(t *testing.T, dsn string) *store.Store {
	t.Helper()
	schema := "t_" + randHex(8)

	// search_path 挂在连接串上，池里每条连接都会带上，不必逐条设置。
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	scoped := fmt.Sprintf("%s%ssearch_path=%s", dsn, sep, schema)

	// 先用原始连接串建 schema，再用带 search_path 的连接串开正式的库句柄。
	admin, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("连接 PostgreSQL 失败: %v", err)
	}
	if _, err := admin.DB().ExecContext(context.Background(),
		"CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("创建测试 schema 失败: %v", err)
	}

	st, err := store.Open(scoped)
	if err != nil {
		_, _ = admin.DB().ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
		t.Fatalf("打开测试 schema 失败: %v", err)
	}
	limitTestConns(st, admin)
	t.Cleanup(func() {
		_ = st.Close()
		// 用例可能留下数据与外键，CASCADE 一次性收干净。
		_, _ = admin.DB().ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})

	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return st
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 随机源不可用时退回固定值：测试环境里这比直接崩掉更有用。
		return "fallback"
	}
	return hex.EncodeToString(b)
}

// IsPostgres 报告当前是否在跑 PostgreSQL，用于跳过只对某一方言有意义的断言。
func IsPostgres() bool {
	return strings.TrimSpace(os.Getenv(EnvKey)) != ""
}

// limitTestConns 把测试库的连接数压到很小。
//
// `go test ./...` 会并行跑多个包的测试二进制，每个都开一个连接池。生产默认的
// 20 条连接乘上七八个包就超过了 PostgreSQL 默认的 100 连接上限，于是某个
// 一次性开二十个并发的用例会随机拿到连接错误 —— 表现成"账号查不到"这种
// 与真正原因毫无关系的样子，极难排查。
//
// 测试并不需要连接数，够用就行。
func limitTestConns(sts ...*store.Store) {
	for _, st := range sts {
		st.DB().SetMaxOpenConns(4)
		st.DB().SetMaxIdleConns(2)
	}
}
