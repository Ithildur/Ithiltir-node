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
	"time"

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
	output := flag.String("output", "/run/ithiltir-node/virt.json", "cache file in a root-owned directory")
	group := flag.String("group", "ithiltir", "group allowed to read the cache")
	guest := flag.Bool("guest", false, "refresh slow Guest Agent IP cache")
	serve := flag.Bool("serve", false, "serve local read-only queries and refresh PVE caches")
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
	if *serve && *guest {
		return fmt.Errorf("--serve and --guest are mutually exclusive")
	}
	if *serve {
		return pve.Serve(ctx, host, *output, virt.SocketPath, pve.GuestDir, gid, version)
	}
	if *guest {
		ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
		defer cancel()
		snapshot, err := virt.Read(*output)
		if err != nil {
			return err
		}
		if snapshot.Host != host {
			return fmt.Errorf("inventory belongs to another host")
		}
		return pve.CollectGuests(ctx, pve.GuestDir, snapshot)
	}
	return pve.Refresh(ctx, host, *output, pve.GuestDir, gid)
}
