package collect

import (
	"strings"

	gnet "github.com/shirou/gopsutil/v3/net"
)

func selectedNICs(preferred ...string) map[string]bool {
	if len(preferred) > 0 {
		explicit := make(map[string]bool, len(preferred))
		for _, name := range preferred {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			explicit[name] = true
		}
		if len(explicit) > 0 {
			return explicit
		}
	}

	ifaces, err := gnet.Interfaces()
	if err != nil {
		return nil
	}

	physicalLike := make(map[string]bool)
	othersWithIP := make(map[string]bool)

	for _, iface := range ifaces {
		isLoopback := false
		isUp := false

		for _, flag := range iface.Flags {
			f := strings.ToLower(flag)
			if f == "loopback" {
				isLoopback = true
			}
			if f == "up" || f == "running" {
				isUp = true
			}
		}

		if isLoopback || !isUp {
			continue
		}

		if isPhysicalNIC(iface.Name, iface.HardwareAddr) {
			physicalLike[iface.Name] = true
			continue
		}

		if len(iface.Addrs) > 0 {
			othersWithIP[iface.Name] = true
		}
	}

	if len(physicalLike) > 0 {
		return physicalLike
	}
	if len(othersWithIP) > 0 {
		return othersWithIP
	}
	return nil
}
