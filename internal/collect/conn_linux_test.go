//go:build linux

package collect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCountTCPUDPFromProcCountsUniqueNetNS(t *testing.T) {
	root := t.TempDir()

	writeNetFiles(t, filepath.Join(root, "net"), 2, 1, 1, 0)
	linkNetNS(t, root, "self", "net:[1]")

	writeNetFiles(t, filepath.Join(root, "1", "net"), 99, 99, 99, 99)
	linkNetNS(t, root, "1", "net:[1]")

	writeNetFiles(t, filepath.Join(root, "2", "net"), 99, 99, 99, 99)
	linkNetNS(t, root, "2", "net:[1]")

	writeNetFiles(t, filepath.Join(root, "3", "net"), 1, 0, 2, 1)
	linkNetNS(t, root, "3", "net:[2]")

	linkNetNS(t, root, "4", "net:[3]")
	writeNetFiles(t, filepath.Join(root, "5", "net"), 1, 0, 0, 1)
	linkNetNS(t, root, "5", "net:[3]")

	tcp, udp := countTCPUDPFromProc(root)
	if tcp != 5 || udp != 5 {
		t.Fatalf("countTCPUDPFromProc() = tcp %d udp %d, want tcp 5 udp 5", tcp, udp)
	}
}

func TestCountTCPUDPFromProcKeepsProcNetWhenNetNSPartial(t *testing.T) {
	root := t.TempDir()

	writeNetFiles(t, filepath.Join(root, "net"), 2, 1, 4, 1)
	linkNetNS(t, root, "self", "net:[1]")

	writeNetFiles(t, filepath.Join(root, "2", "net"), 1, 0, 0, 1)
	linkNetNS(t, root, "2", "net:[2]")

	linkNetNS(t, root, "3", "net:[3]")

	tcp, udp := countTCPUDPFromProc(root)
	if tcp != 4 || udp != 6 {
		t.Fatalf("countTCPUDPFromProc() = tcp %d udp %d, want tcp 4 udp 6", tcp, udp)
	}
}

func TestCountTCPUDPFromProcFallsBackToProcNet(t *testing.T) {
	root := t.TempDir()
	writeNetFiles(t, filepath.Join(root, "net"), 2, 1, 4, 1)

	tcp, udp := countTCPUDPFromProc(root)
	if tcp != 3 || udp != 5 {
		t.Fatalf("countTCPUDPFromProc() = tcp %d udp %d, want tcp 3 udp 5", tcp, udp)
	}
}

func linkNetNS(t *testing.T, root, pid, ns string) {
	t.Helper()

	nsDir := filepath.Join(root, pid, "ns")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", nsDir, err)
	}
	if err := os.Symlink(ns, filepath.Join(nsDir, "net")); err != nil {
		t.Fatalf("symlink netns for pid %s: %v", pid, err)
	}
}

func writeNetFiles(t *testing.T, dir string, tcp int, tcp6 int, udp int, udp6 int) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	writeProcNetFile(t, filepath.Join(dir, "tcp"), tcp)
	writeProcNetFile(t, filepath.Join(dir, "tcp6"), tcp6)
	writeProcNetFile(t, filepath.Join(dir, "udp"), udp)
	writeProcNetFile(t, filepath.Join(dir, "udp6"), udp6)
}

func writeProcNetFile(t *testing.T, path string, rows int) {
	t.Helper()

	body := "sl local_address rem_address st\n"
	for range rows {
		body += "0: 00000000:0000 00000000:0000 00\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
