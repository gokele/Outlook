//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// install 用新文件替换正在运行的自己，返回备份路径。
//
// Windows 不允许覆盖正在运行的 exe（文件被映射时带着共享写锁），
// 只能先把自己改名让路 —— 改名对运行中的 exe 是允许的 —— 再把新文件
// 放到原来的位置。放不回去时要把自己改回来，否则用户会落得一个
// 没有可执行文件的目录。
func install(tmpPath, exePath string) (string, error) {
	backup := exePath + backupSuffix
	_ = os.Remove(backup)
	if err := os.Rename(exePath, backup); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("重命名当前程序失败（是否被杀毒软件锁定？）: %w", err)
	}
	if err := os.Rename(tmpPath, exePath); err != nil {
		_ = os.Rename(backup, exePath)
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("替换二进制失败，已回滚: %w", err)
	}
	return backup, nil
}

// Relaunch 启动 execPath 处的新版本并退出当前进程。
//
// execPath 必须是替换二进制之前捕获的路径，理由见 install_unix.go 的说明。
//
// Windows 没有 execve，换不了进程映像，只能"起一个新的、自己退出"。
// 与 Unix 版有两处差别，调用方会看到：进程号会变；旧进程要先释放监听端口，
// 新进程才绑得上，因此这里退出前留了一小段时间。
func Relaunch(execPath string) error {
	if err := checkExecutable(execPath); err != nil {
		return err
	}
	cmd := exec.Command(execPath, os.Args[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动新版本失败: %w", err)
	}
	// 不 Wait：新进程要独立活下去，当前进程随即退出把端口让出来。
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}
