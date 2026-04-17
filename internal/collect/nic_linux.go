//go:build linux

package collect

func isPhysicalNIC(ifaceName, _ string) bool {
	base := "/sys/class/net/" + ifaceName
	if pathExists(base + "/bridge") {
		return false
	}
	if pathExists(base + "/device") {
		return true
	}
	if pathExists(base + "/bonding") {
		return true
	}
	return false
}
