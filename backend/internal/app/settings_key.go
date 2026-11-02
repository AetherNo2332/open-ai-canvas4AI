package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

func publishSettingsKey(path string, key []byte, link func(string, string) error) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".settings-key-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(key); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Link publishes complete bytes without replacing another process's key.
	if err := link(file.Name(), path); err != nil {
		if !errors.Is(err, syscall.ENOTSUP) && !errors.Is(err, syscall.EPERM) {
			return err
		}
		// Some filesystems prohibit hard links. O_EXCL still prevents replacement,
		// but readers may see an incomplete file and a crash may leave it invalid.
		fallback, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		defer fallback.Close()
		if _, err := fallback.Write(key); err != nil {
			return err
		}
		if err := fallback.Sync(); err != nil {
			return err
		}
		if err := fallback.Close(); err != nil {
			return err
		}
	}
	// Persist the published name, not only the inode's contents.
	// Windows 打开的目录句柄不支持目录级 Sync（"sync <dir>: Access is denied"），
	// 文件本体已经 fsync，目录冲刷只在支持该语义的平台执行，保持 best-effort 语义。
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		defer dir.Close()
		return dir.Sync()
	}
	return nil
}
