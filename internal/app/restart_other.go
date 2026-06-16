//go:build !linux && !darwin

package app

func restartForUpdate() error {
	return nil
}
