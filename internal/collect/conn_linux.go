//go:build linux

package collect

import (
	"bufio"
	"os"
)

func countProcNetFile(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if n > 0 {
		n--
	}
	return n, nil
}

func countTCPUDP() (tcp int, udp int) {
	if n, err := countProcNetFile("/proc/net/tcp"); err == nil {
		tcp += n
	}
	if n, err := countProcNetFile("/proc/net/tcp6"); err == nil {
		tcp += n
	}
	if n, err := countProcNetFile("/proc/net/udp"); err == nil {
		udp += n
	}
	if n, err := countProcNetFile("/proc/net/udp6"); err == nil {
		udp += n
	}
	return tcp, udp
}
