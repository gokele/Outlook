package updater

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// pendingSuffix 是"更新后待验证"标记的后缀。
const pendingSuffix = ".pending"

// MarkPending 在替换完二进制、准备重启之前写下待验证标记。
//
// 内容是"带着这个标记启动过几次"，初始为 0 —— 此刻新版本还一次都没启动过。
func MarkPending(exePath string) error {
	return os.WriteFile(exePath+pendingSuffix, []byte("0"), 0o600)
}

// MarkHealthy 宣告新版本确实活到了对外服务那一刻。
//
// **调用时机是关键：必须在服务真正开始对外提供服务之后**，不能一进 main 就调。
// 一进 main 就调等于没有验证 —— 那时还没绑端口、没跑迁移，什么都没证明。
//
// 先删标记再删备份：备份可能因为磁盘或权限问题压根没建成，那种情况下标记
// 若留着，下次启动虽然不会误回滚（没有备份可回），文件却会一直躺在那儿。
func MarkHealthy(exePath string) (cleaned bool) {
	_ = os.Remove(exePath + pendingSuffix)
	backup := exePath + backupSuffix
	if _, err := os.Stat(backup); err != nil {
		return false // 没有备份 = 这次不是更新后的首次启动
	}
	return os.Remove(backup) == nil
}

// RollbackIfStale 在启动早期检查：上一次更新装上去的版本，是不是根本没跑起来。
//
// 判据是备份与待验证标记同时存在。返回 true 表示已经换回旧版本，
// 调用方应当立即用换回来的二进制重启。
//
// 关于那个启动计数 —— 这是整段逻辑里唯一不直观的地方，也是最容易写错的地方：
//
// 本函数跑在启动早期，而 MarkHealthy 要等端口监听成功才执行。所以在这里，
// "标记还在"永远是真的 —— 包括新版本正常启动的那一次。若只看标记在不在就回滚，
// 结果是每一次自更新都会在新版本第一次启动时被判定为失败、静默换回旧版，
// 用户看到的是界面一直转"正在重启并加载新版本…"直到超时，而版本从未变过。
//
// 计数把两种情况分开：
//
//	0  = 新版本头一回启动，放行，让它自己跑到 MarkHealthy 去删标记
//	≥1 = 上一回已经带着标记启动过，却没能撑到 MarkHealthy，这才是真的起不来
func RollbackIfStale(exePath string) (rolledBack bool, note string) {
	backup := exePath + backupSuffix
	pending := exePath + pendingSuffix

	if _, err := os.Stat(backup); err != nil {
		return false, "" // 没有备份，说明上一次不是更新
	}

	raw, err := os.ReadFile(pending)
	if err != nil {
		// 有备份但没有标记：说明上次是正常起来过的（只是 MarkHealthy 没来得及删备份）。
		// 清掉备份即可，绝不能回滚 —— 那会把用户刚更新好的版本又换回旧的。
		_ = os.Remove(backup)
		return false, ""
	}

	if boots := parseBoots(raw); boots < 1 {
		next := []byte(strconv.Itoa(boots + 1))
		if werr := os.WriteFile(pending, next, 0o600); werr != nil {
			// 写不进去就不能再放行：下次启动还会读到 0，永远滚不动，
			// 等于回滚保护彻底失效。宁可这一次多回滚一遍。
			return doRollback(exePath, backup, pending,
				"无法更新待验证标记，按保守策略回滚: "+werr.Error())
		}
		return false, "新版本首次启动，回滚保护已就绪（启动成功后自动解除）"
	}

	return doRollback(exePath, backup, pending,
		"检测到上一次更新后未能正常启动")
}

// doRollback 把备份换回现役位置。
func doRollback(exePath, backup, pending, why string) (bool, string) {
	_ = os.Remove(pending)
	if err := os.Rename(backup, exePath); err != nil {
		return false, fmt.Sprintf("%s，但回滚失败，请手动把 %s 改名为 %s：%v",
			why, backup, exePath, err)
	}
	return true, why + "，已换回更新前的版本"
}

// parseBoots 解析标记里的启动计数。解析不出来时按 0 处理 ——
// 那多半是文件被截断，此时放行一次比直接回滚更符合预期。
func parseBoots(raw []byte) int {
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
