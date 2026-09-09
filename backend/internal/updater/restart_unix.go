//go:build !windows

package updater

import (
	"os"
	"syscall"
)

// Relaunch 用新装好的二进制替换当前进程映像。
//
// 走 execve 而不是"退出后等 systemd 拉起"，是为了让一键更新在任何部署方式下
// 都能自己完成：进程号不变，不依赖任何进程守护，也就不存在"退出了没人拉起"
// 这种把更新变成停服的情况。
//
// 监听套接字不会被继承（Go 创建的 fd 都带 CLOEXEC），exec 的瞬间端口被释放，
// 新映像随即重新绑定。中间有个极短的窗口连不上，属于重启的固有代价。
//
// 正常情况下这个函数不返回 —— 当前程序已经不存在了。返回即意味着 exec 失败，
// 调用方应当退回到"退出并由守护进程拉起"的老路。
func Relaunch() error {
	self, err := SelfPath()
	if err != nil {
		return err
	}
	// 原样传递参数与环境：更新只换二进制，不该顺手改变启动方式。
	argv := append([]string{self}, os.Args[1:]...)
	return syscall.Exec(self, argv, os.Environ())
}
