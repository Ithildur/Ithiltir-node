//go:build !linux

package collect

import "strings"

func isPhysicalNIC(_ string, hw string) bool {
	hw = strings.TrimSpace(hw)
	if hw == "" {
		return false
	}
	hw = strings.ReplaceAll(hw, ":", "")
	hw = strings.ReplaceAll(hw, "-", "")
	if hw == "" {
		return false
	}
	allZero := true
	for i := 0; i < len(hw); i++ {
		if hw[i] != '0' && hw[i] != ' ' {
			allZero = false
			break
		}
	}
	return !allZero
}
