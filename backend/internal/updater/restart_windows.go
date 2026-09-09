//go:build windows

package updater

import (
	"os"
	"os/exec"
	"time"
)

// Relaunch 启动新版本并退出当前进程。
//
// Windows 没有 execve，换不了进程映像，只能"起一个新的、自己退出"。
// 与 Unix 版的差别有两处，都会被调用方看到：进程号会变；旧进程必须先
// 释放监听端口，新进程才绑得上，因此这里退出前留了一小段时间。
//
// 服务方式（NSSM、sc.exe 之类）运行时，更稳妥的做法是让服务管理器重启，
// 但那需要提权且各家接口不同；直接拉一个新进程在两种场景下都能work。
func Relaunch() error {
	self, err := SelfPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, os.Args[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// 不 Wait：子进程要独立活下去，父进程随即退出把端口让出来。
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}
