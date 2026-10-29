//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"Ithiltir-node/internal/pve"
	"Ithiltir-node/internal/virt"
)

var version = "unknown"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "print release version")
	group := flag.String("group", "ithiltir", "group allowed to query the helper socket")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("pve-cache requires root")
	}
	g, err := user.LookupGroup(*group)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return err
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	host, _, _ = strings.Cut(host, ".")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return pve.Serve(ctx, host, virt.SocketPath, pve.GuestDir, gid, version)
}
