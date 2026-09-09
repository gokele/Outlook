package updater

import (
	"fmt"
	"os"
	"path/filepath"
)

// backupSuffix 是上一版二进制的后缀。新版本起不来时，把它改名回去即可。
const backupSuffix = ".old"

// SelfPath 返回当前二进制的路径。
//
// **必须在替换二进制之前调用。** Linux 上它读的是 /proc/self/exe，
// 而那是跟随 inode 的：安装之后再问，得到的是备份文件而不是新版本。
func SelfPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", ErrNotSupported
	}
	// 解开符号链接：有的部署会用 /usr/local/bin/api 软链到实际位置，
	// 而替换要作用在真实文件上。
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return "", ErrNotSupported
	}
	return self, nil
}

// checkExecutable 确认目标确实是一个能执行的文件。
//
// 宁可在这里失败、退回给进程守护拉起，也不要 exec 到一个不存在或没有执行位的
// 文件 —— 那会让进程直接消失，而不是重启。
func checkExecutable(path string) error {
	if path == "" {
		return fmt.Errorf("重启目标路径为空")
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("重启目标不可用: %w", err)
	}
	if st.IsDir() {
		return fmt.Errorf("重启目标 %s 不是文件", path)
	}
	return nil
}
