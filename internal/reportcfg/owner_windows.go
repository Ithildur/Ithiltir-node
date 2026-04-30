//go:build windows

package reportcfg

import "os"

func checkMode(_ os.FileInfo) error {
	return nil
}

func chownLike(_, _ string) error {
	return nil
}
