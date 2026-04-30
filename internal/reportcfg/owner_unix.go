//go:build !windows

package reportcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func checkMode(info os.FileInfo) error {
	if info.Mode().Perm() != fileMode {
		return fmt.Errorf("report config mode must be %o", fileMode)
	}
	return nil
}

func chownLike(path, tmpName string) error {
	if os.Geteuid() != 0 {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat report config owner: %w", err)
		}
		info, err = os.Stat(filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("stat report config dir owner: %w", err)
		}
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if err := os.Chown(tmpName, int(stat.Uid), int(stat.Gid)); err != nil {
		return fmt.Errorf("chown report config temp: %w", err)
	}
	return nil
}
