//go:build !windows

package updater

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// install 用新文件替换正在运行的自己，返回备份路径。
//
// Unix 上 rename 允许覆盖正在执行的文件：内核按 inode 引用可执行映像，
// 旧进程会继续用旧 inode 跑到退出为止。因此这里**直接覆盖 exePath**，
// 而不是"先把自己改名让路再放新的" —— 后者中间有一瞬间 exePath 根本不存在，
// 那时断电或崩溃，机器上就没有可执行文件了。
//
// 备份用硬链接：不额外占十几 MB，也和 rename 一样只是同目录内的元数据操作。
func install(tmpPath, exePath string) (string, error) {
	// 保留原有权限位。有人可能特意设过更严格的 750，或者加了 setgid。
	if fi, err := os.Stat(exePath); err == nil {
		_ = os.Chmod(tmpPath, fi.Mode().Perm())
	} else {
		_ = os.Chmod(tmpPath, 0o755)
	}

	backup := exePath + backupSuffix
	_ = os.Remove(backup)
	if err := os.Link(exePath, backup); err != nil {
		// 硬链接建不了（跨设备、某些网络文件系统）就退回复制。
		// 总之必须留下后路：新版本起不来时，这是唯一能换回去的东西。
		if cerr := copyFile(exePath, backup); cerr != nil {
			_ = os.Remove(tmpPath)
			return "", fmt.Errorf("备份当前版本失败，已中止更新: %w", cerr)
		}
	}

	if err := os.Rename(tmpPath, exePath); err != nil {
		_ = os.Remove(tmpPath)
		_ = os.Remove(backup)
		return "", fmt.Errorf("替换二进制失败: %w", err)
	}
	return backup, nil
}

// Relaunch 用 execPath 处的二进制替换当前进程映像。
//
// **execPath 必须是替换二进制之前捕获的路径，不能在这里现问 os.Executable()。**
// Linux 上 os.Executable() 读的是 /proc/self/exe，它跟随的是 inode 而不是路径。
// 安装更新会让 exePath 指向新的 inode，而旧 inode 仍被备份的硬链接引用着，
// 于是此刻再问，得到的是备份文件的路径 —— execve 会忠实地把旧版本重新拉起来。
// 表面上进程重启了、PID 也没变，唯独版本没动，看起来就是"更新完还得手动重启"。
//
// 走 execve 而不是"退出后等 systemd 拉起"：PID 与整套 fd 都保持不变，
// 对按 PID 管理的进程守护是无感的，也就不存在"退出了没人拉起"这种把更新
// 变成停服的情况。
//
// 监听套接字不会被继承（Go 创建的 fd 都带 CLOEXEC），exec 的瞬间端口被释放，
// 新映像随即重新绑定。中间有个极短的窗口连不上，属于重启的固有代价。
//
// 成功时这个函数不返回 —— 当前程序已经不存在了。
func Relaunch(execPath string) error {
	if err := checkExecutable(execPath); err != nil {
		return err
	}
	argv := append([]string{execPath}, os.Args[1:]...)
	return syscall.Exec(execPath, argv, os.Environ())
}

// copyFile 在硬链接不可用时退化为内容拷贝。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
