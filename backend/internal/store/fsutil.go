package store

import (
	"os"
	"path/filepath"
)

// ensureDir 确保 SQLite 文件所在目录存在。
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o750)
}
