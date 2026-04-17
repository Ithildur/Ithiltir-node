package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"Ithiltir-node/internal/cli"
	"Ithiltir-node/internal/collect"
	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/push"
	"Ithiltir-node/internal/server"
)

func Run(rawArgs []string) int {
	args, preferredNICs, debug, showVersion, requireHTTPS, warnings := cli.Parse(rawArgs)
	for _, warning := range warnings {
		log.Printf("WARN: %s", warning)
	}
	if showVersion {
		fmt.Println(VersionString())
		return 0
	}

	cfg := collect.Config{}
	if len(preferredNICs) > 0 {
		cfg.PreferredNICs = preferredNICs
	}
	cfg.Debug = debug

	if len(args) == 0 || args[0] == "serve" {
		return runServe(args, cfg, debug)
	}

	if args[0] == "push" {
		return runPush(args, cfg, debug, requireHTTPS)
	}

	printUsage()
	return 1
}

func runServe(args []string, cfg collect.Config, debug bool) int {
	listenIP := "0.0.0.0"
	listenPort := "9100"

	if envHost := os.Getenv("NODE_HOST"); envHost != "" {
		listenIP = envHost
	}
	if envPort := os.Getenv("NODE_PORT"); envPort != "" {
		listenPort = envPort
	}

	if len(args) >= 2 {
		listenIP = args[1]
	}
	if len(args) >= 3 {
		listenPort = args[2]
	}

	fast := 3 * time.Second
	s := collect.NewSampler(fast, 2*time.Second, 5*time.Second, cfg, Version)
	s.Start()
	defer s.Stop()

	srv, addr := server.NewServer(listenIP, listenPort, s, debug)
	log.Printf("Ithiltir-node (serve mode) listening on %s", addr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		_ = srv.Close()
		return exitCodeForSignal(sig)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
			return 1
		}
		return 0
	}
}

func runPush(args []string, cfg collect.Config, debug bool, requireHTTPS bool) int {
	if len(args) < 4 {
		fmt.Println("Usage: ./node push <dash_host> <dash_port> <secret> [interval_seconds] [--net iface1,iface2] [--debug] [--require-https]")
		return 1
	}
	dashHost := args[1]
	dashPort := args[2]
	secret := args[3]

	intervalSec := 3
	if len(args) >= 5 {
		if v, err := strconv.Atoi(args[4]); err == nil && v > 0 {
			intervalSec = v
		} else {
			log.Printf("WARN: invalid interval_seconds %q, using default %d", args[4], intervalSec)
		}
	}
	interval := time.Duration(intervalSec) * time.Second

	s := collect.NewSampler(interval, 2*time.Second, 5*time.Second, cfg, Version)
	s.Start()
	defer s.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listenIP := "127.0.0.1"
	listenPort := "9100"
	if envPort := os.Getenv("NODE_PORT"); envPort != "" {
		listenPort = envPort
	}

	cache := push.NewCache()
	localReport := func() *metrics.NodeReport {
		if cached := cache.Get(); cached != nil {
			return cached
		}
		m := s.Snapshot()
		if m == nil {
			return nil
		}
		report := metrics.NewNodeReport(s.Version(), s.Hostname(), s.Time(), m)
		return &report
	}
	srv, addr := server.NewPushServer(listenIP, listenPort, localReport)
	log.Printf("Ithiltir-node (push mode) local metrics on %s/", addr)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("push metrics server error: %v", err)
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- push.StartWithCache(ctx, dashHost, dashPort, secret, interval, s, debug, requireHTTPS, cache)
	}()

	select {
	case sig := <-sigCh:
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		shutdownCancel()
		_ = srv.Close()
		return exitCodeForSignal(sig)
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("push error: %v", err)
			return 1
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		shutdownCancel()
		_ = srv.Close()
		return 0
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  Serve:")
	fmt.Println("    ./node [--net iface1,iface2] [--debug]")
	fmt.Println("    ./node serve [listen_ip] [listen_port] [--net iface1,iface2] [--debug]")
	fmt.Println()
	fmt.Println("  Push:")
	fmt.Println("    ./node push <dash_host> <dash_port> <secret> [interval_seconds] [--net iface1,iface2] [--debug] [--require-https]")
	fmt.Println()
	fmt.Println("  Version:")
	fmt.Println("    ./node --version")
	fmt.Println("    ./node -v")
}
