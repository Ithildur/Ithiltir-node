//go:build !linux

package collect

import gnet "github.com/shirou/gopsutil/v3/net"

func countTCPUDP() (tcp int, udp int) {
	if conns, err := gnet.Connections("tcp"); err == nil {
		tcp = len(conns)
	}
	if conns, err := gnet.Connections("udp"); err == nil {
		udp = len(conns)
	}
	return tcp, udp
}
