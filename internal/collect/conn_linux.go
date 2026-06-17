//go:build linux

package collect

import (
	"bufio"
	"os"
	"path/filepath"
	"time"

	"Ithiltir-node/internal/conncache"
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

func countTCPUDPInNetDir(dir string) (tcp int, udp int, ok bool) {
	if n, err := countProcNetFile(filepath.Join(dir, "tcp")); err == nil {
		tcp += n
		ok = true
	}
	if n, err := countProcNetFile(filepath.Join(dir, "tcp6")); err == nil {
		tcp += n
		ok = true
	}
	if n, err := countProcNetFile(filepath.Join(dir, "udp")); err == nil {
		udp += n
		ok = true
	}
	if n, err := countProcNetFile(filepath.Join(dir, "udp6")); err == nil {
		udp += n
		ok = true
	}
	return tcp, udp, ok
}

func countTCPUDPFromProc(root string) (tcp int, udp int) {
	tcp, udp, ok := countTCPUDPInNetDir(filepath.Join(root, "net"))
	seen := make(map[string]struct{})
	if ok {
		markCurrentNetNS(root, seen)
	}

	nsTCP, nsUDP := countTCPUDPByNetNS(root, seen)
	tcp += nsTCP
	udp += nsUDP
	return tcp, udp
}

func markCurrentNetNS(root string, seen map[string]struct{}) {
	ns, err := os.Readlink(filepath.Join(root, "self", "ns", "net"))
	if err != nil {
		return
	}
	seen[ns] = struct{}{}
}

func countTCPUDPByNetNS(root string, seen map[string]struct{}) (tcp int, udp int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0
	}

	for _, entry := range entries {
		if !entry.IsDir() || !isProcPID(entry.Name()) {
			continue
		}

		pidRoot := filepath.Join(root, entry.Name())
		ns, err := os.Readlink(filepath.Join(pidRoot, "ns", "net"))
		if err != nil {
			continue
		}
		if _, exists := seen[ns]; exists {
			continue
		}

		nsTCP, nsUDP, read := countTCPUDPInNetDir(filepath.Join(pidRoot, "net"))
		if !read {
			continue
		}

		seen[ns] = struct{}{}
		tcp += nsTCP
		udp += nsUDP
	}
	return tcp, udp
}

func isProcPID(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func countTCPUDP() (tcp int, udp int) {
	return countTCPUDPWithCache(conncache.DefaultPath(), "/proc", time.Now().UTC())
}

func countTCPUDPWithCache(cachePath, procRoot string, now time.Time) (tcp int, udp int) {
	if tcp, udp, ok := conncache.Read(cachePath, now); ok {
		return tcp, udp
	}
	return countTCPUDPFromProc(procRoot)
}
