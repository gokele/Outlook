package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// preflightTimeout 是试运行的超时。-version 是立即返回的，
// 超过这个时间说明新二进制在启动阶段就卡住了，同样算不可用。
const preflightTimeout = 20 * time.Second

// preflight 在替换现役二进制之前，先把下载来的这个跑一次。
//
// **这一步是回滚机制的前提，不是锦上添花。**
//
// 事后回滚（见 rollback.go）依赖"新版本起不来 → 进程被重新拉起 → 下次启动时
// 换回旧版"。但我们的重启走 execve，没有进程守护时新版本一崩就没人再拉起它，
// 回滚逻辑根本没机会执行 —— 服务就那么停在那里。
//
// 而"二进制根本跑不起来"恰恰是最常见的一类失败：下载损坏、架构不匹配、
// 交叉编译时链进了本机的动态库。这类问题跑一次 -version 就能发现，
// 代价是几十毫秒，换来的是"现役二进制一个字节都不会被动"。
//
// 跑 -version 而不是完整启动：它不碰数据库、不绑端口、不产生任何副作用，
// 但要走完 Go 运行时初始化与全部包的 init，足以证明这个文件是能执行的。
func preflight(ctx context.Context, path, wantVersion string) error {
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("无法给新版本加执行权限: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "-version")
	// 不继承当前进程的环境：新版本此刻还不该读到任何真实配置，
	// 万一它在启动阶段就去连数据库，那不是我们想在这里触发的事。
	cmd.Env = []string{}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("新版本试运行超时，已放弃更新（现役版本未被改动）")
		}
		return fmt.Errorf("新版本无法执行，已放弃更新（现役版本未被改动）: %w，输出: %s",
			err, truncate(string(out), 200))
	}

	// 顺带核对版本号：下载到的资产若与 release 标签对不上，
	// 说明发布流程出了问题，装上去只会造成"更新了但版本没变"的困惑。
	got := strings.TrimSpace(string(out))
	if wantVersion != "" && !strings.Contains(got, strings.TrimPrefix(wantVersion, "v")) {
		return fmt.Errorf("新版本自报的版本号与发布标签不符，已放弃更新（期望含 %s，实际 %q）",
			wantVersion, truncate(got, 100))
	}
	return nil
}

// truncate 截断过长文本，避免把一整屏输出塞进错误信息。
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
