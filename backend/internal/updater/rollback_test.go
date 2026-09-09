package updater

import (
	"os"
	"path/filepath"
	"testing"
)

// setup 造一个"刚更新完"的现场：现役是新版本，备份是旧版本。
func setup(t *testing.T) (exe, backup, pending string) {
	t.Helper()
	dir := t.TempDir()
	exe = filepath.Join(dir, "api")
	backup = exe + backupSuffix
	pending = exe + pendingSuffix
	if err := os.WriteFile(exe, []byte("新版本"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("旧版本"), 0o755); err != nil {
		t.Fatal(err)
	}
	return
}

// TestFirstBootIsAllowed 是这套逻辑里最容易写错的一条。
//
// RollbackIfStale 跑在启动早期，而 MarkHealthy 要等端口绑上才执行 ——
// 所以在检查的那一刻，"标记还在"永远是真的，包括新版本正常启动的那一次。
// 只看标记在不在就回滚的话，每一次自更新都会在新版本第一次启动时被判失败、
// 静默换回旧版，而用户看到的是版本从未变过。
func TestFirstBootIsAllowed(t *testing.T) {
	exe, _, pending := setup(t)
	if err := MarkPending(exe); err != nil {
		t.Fatal(err)
	}

	rolledBack, note := RollbackIfStale(exe)
	if rolledBack {
		t.Fatal("新版本的第一次启动绝不能回滚")
	}
	if note == "" {
		t.Error("应说明回滚保护已就绪")
	}
	if got, _ := os.ReadFile(exe); string(got) != "新版本" {
		t.Fatalf("现役应仍是新版本，实际 %q", got)
	}
	// 计数应当被递增，第二次启动才判失败。
	if got, _ := os.ReadFile(pending); string(got) != "1" {
		t.Fatalf("启动计数应递增到 1，实际 %q", got)
	}
}

// TestSecondBootRollsBack 校验第二次带着标记启动才判定为真的起不来。
func TestSecondBootRollsBack(t *testing.T) {
	exe, backup, pending := setup(t)
	_ = MarkPending(exe)

	// 第一次启动：放行
	if rolledBack, _ := RollbackIfStale(exe); rolledBack {
		t.Fatal("第一次不该回滚")
	}
	// 新版本没能撑到 MarkHealthy，进程被重新拉起 —— 第二次启动
	rolledBack, note := RollbackIfStale(exe)
	if !rolledBack {
		t.Fatal("第二次带着标记启动应判为起不来并回滚")
	}
	if note == "" {
		t.Error("回滚应给出说明")
	}
	if got, _ := os.ReadFile(exe); string(got) != "旧版本" {
		t.Fatalf("应已换回旧版本，实际 %q", got)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("回滚后备份应已被移走")
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Error("回滚后标记应被清掉")
	}
}

// TestHealthyClearsProtection 校验新版本活到对外服务后，保护被正确解除。
func TestHealthyClearsProtection(t *testing.T) {
	exe, backup, pending := setup(t)
	_ = MarkPending(exe)
	_, _ = RollbackIfStale(exe) // 第一次启动

	if !MarkHealthy(exe) {
		t.Fatal("应清理掉备份")
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Error("标记应被删除")
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("备份应被删除")
	}

	// 保护解除后再启动，不该有任何动作。
	if rolledBack, note := RollbackIfStale(exe); rolledBack || note != "" {
		t.Fatalf("保护已解除，不该再有动作: rolledBack=%v note=%q", rolledBack, note)
	}
}

// TestBackupWithoutPendingIsCleanedNotRolledBack 校验一个危险的边界：
// 有备份但没有标记，说明上次是正常起来过的（只是备份没删干净）。
// 这时若回滚，会把用户刚更新好的版本又换回旧的。
func TestBackupWithoutPendingIsCleanedNotRolledBack(t *testing.T) {
	exe, backup, _ := setup(t)
	// 不写 pending

	rolledBack, _ := RollbackIfStale(exe)
	if rolledBack {
		t.Fatal("没有待验证标记时绝不能回滚")
	}
	if got, _ := os.ReadFile(exe); string(got) != "新版本" {
		t.Fatalf("现役应保持新版本，实际 %q", got)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("多余的备份应被清掉")
	}
}

// TestNoBackupIsNoop 校验没有备份时什么都不做 —— 那说明上次不是更新。
func TestNoBackupIsNoop(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "api")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rolledBack, note := RollbackIfStale(exe); rolledBack || note != "" {
		t.Fatalf("没有备份时不该有任何动作: %v %q", rolledBack, note)
	}
}

// TestParseBoots 校验计数解析：坏数据按 0 处理，即放行一次。
// 直接回滚更保守，但会把一次本来正常的更新滚掉。
func TestParseBoots(t *testing.T) {
	cases := map[string]int{"0": 0, "1": 1, "3": 3, " 2 \n": 2, "": 0, "abc": 0, "-1": 0}
	for in, want := range cases {
		if got := parseBoots([]byte(in)); got != want {
			t.Errorf("%q 期望 %d，实际 %d", in, want, got)
		}
	}
}

// TestPreflightRejectsBadBinary 校验跑不起来的东西压根不会被装上。
//
// 这一步是回滚机制的前提：事后回滚依赖"起不来就重启后换回去"，
// 而我们的重启走 execve，没有进程守护时新版本一崩就没人再拉起它 ——
// 服务会那么停在那里。所以"根本不是可执行文件"这类失败必须在换之前拦住。
func TestPreflightRejectsBadBinary(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "notabinary")
	if err := os.WriteFile(bad, []byte("这不是可执行文件"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := preflight(t.Context(), bad, "v1.0.0"); err == nil {
		t.Fatal("跑不起来的文件必须被拒绝")
	}
}

// TestPreflightAcceptsGoodBinary 校验能跑且版本号对得上的可以放行。
func TestPreflightAcceptsGoodBinary(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "fake")
	// 用一个 shell 脚本冒充：preflight 只关心"能执行且 -version 的输出含目标版本"。
	script := "#!/bin/sh\necho 'outlook-console api v9.9.9'\n"
	if err := os.WriteFile(good, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := preflight(t.Context(), good, "v9.9.9"); err != nil {
		t.Fatalf("正常的二进制应放行: %v", err)
	}
}

// TestPreflightRejectsVersionMismatch 校验版本对不上也拦下来。
// 资产与发布标签对不上说明发布流程出了问题，装上去只会造成
// "更新了但版本没变"的困惑。
func TestPreflightRejectsVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fake")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho 'api v1.0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := preflight(t.Context(), p, "v2.0.0"); err == nil {
		t.Fatal("版本号对不上必须被拒绝")
	}
}
