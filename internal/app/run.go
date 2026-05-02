package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"Ithiltir-node/internal/cli"
	"Ithiltir-node/internal/collect"
	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/push"
	"Ithiltir-node/internal/reportcfg"
	"Ithiltir-node/internal/selfupdate"
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

	if args[0] == "report" {
		return runReport(args, requireHTTPS)
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
	if len(args) > 2 {
		fmt.Println("Usage: ./node push [interval_seconds] [--net iface1,iface2] [--debug] [--require-https]")
		fmt.Println("Report targets are read from " + reportcfg.DefaultPath())
		return 1
	}

	intervalSec := 3
	if len(args) == 2 {
		if v, err := strconv.Atoi(args[1]); err == nil && v > 0 {
			intervalSec = v
		} else {
			log.Printf("WARN: invalid interval_seconds %q, using default %d", args[1], intervalSec)
		}
	}
	interval := time.Duration(intervalSec) * time.Second

	reportPath := reportcfg.DefaultPath()
	reportConfig, err := reportcfg.Load(reportPath)
	if err != nil {
		log.Printf("report config error: %v", err)
		return 1
	}

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
		errCh <- push.StartWithCache(ctx, reportConfig.Targets, interval, s, debug, requireHTTPS, cache)
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
		if errors.Is(err, selfupdate.ErrRestart) {
			cancel()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(shutdownCtx)
			shutdownCancel()
			_ = srv.Close()
			return 0
		}
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

func runReport(args []string, requireHTTPS bool) int {
	if len(args) < 2 {
		printReportUsage()
		return 1
	}

	path := reportcfg.DefaultPath()
	switch args[1] {
	case "install":
		if len(args) != 4 {
			fmt.Println("Usage: ./node report install <url> <key> [--require-https]")
			return 1
		}
		cfg, err := reportcfg.Load(path)
		if err != nil {
			log.Printf("report config error: %v", err)
			return 1
		}
		return saveReportInstall(path, cfg, args[2], args[3], requireHTTPS)

	case "remove":
		if len(args) != 3 {
			fmt.Println("Usage: ./node report remove <id>")
			return 1
		}
		id, err := strconv.Atoi(args[2])
		if err != nil || id <= 0 {
			log.Printf("report remove error: invalid id %q", args[2])
			return 1
		}
		cfg, err := reportcfg.Load(path)
		if err != nil {
			log.Printf("report config error: %v", err)
			return 1
		}
		next, err := reportcfg.Remove(cfg, id)
		if err != nil {
			log.Printf("report remove error: %v", err)
			return 1
		}
		if err := reportcfg.Save(path, next); err != nil {
			log.Printf("report config write error: %v", err)
			return 1
		}
		return 0

	case "update":
		if len(args) != 4 {
			fmt.Println("Usage: ./node report update <id> <key>")
			return 1
		}
		id, err := strconv.Atoi(args[2])
		if err != nil || id <= 0 {
			log.Printf("report update error: invalid id %q", args[2])
			return 1
		}
		cfg, err := reportcfg.Load(path)
		if err != nil {
			log.Printf("report config error: %v", err)
			return 1
		}
		next, err := reportcfg.UpdateKey(cfg, id, args[3])
		if err != nil {
			log.Printf("report update error: %v", err)
			return 1
		}
		if err := reportcfg.Save(path, next); err != nil {
			log.Printf("report config write error: %v", err)
			return 1
		}
		printTarget(targetByID(next, id))
		return 0

	case "list":
		if len(args) != 2 {
			fmt.Println("Usage: ./node report list")
			return 1
		}
		cfg, err := reportcfg.Load(path)
		if err != nil {
			log.Printf("report config error: %v", err)
			return 1
		}
		for _, target := range cfg.Targets {
			fmt.Printf("%d\t%s\t%s\n", target.ID, target.URL, target.ServerInstallID)
		}
		return 0
	default:
		printReportUsage()
		return 1
	}
}

func saveReportInstall(path string, cfg reportcfg.Config, endpoint, key string, requireHTTPS bool) int {
	endpoint = strings.TrimSpace(endpoint)
	key = strings.TrimSpace(key)
	urlTarget, urlTargetOK := targetByURLKey(cfg, endpoint, key)
	if urlTargetOK && strings.TrimSpace(urlTarget.ServerInstallID) != "" {
		printTarget(urlTarget)
		return 0
	}

	identity, err := resolveReportIdentity(endpoint, key, requireHTTPS)
	if err != nil {
		log.Printf("report identity error: %v", err)
		return 1
	}
	printIdentityNotice(identity)
	incoming := reportcfg.Target{
		URL:             endpoint,
		Key:             key,
		ServerInstallID: identity.InstallID,
	}
	if existing, ok := duplicateTarget(cfg, identity.InstallID); ok {
		if sameReportTarget(existing, incoming) {
			printTarget(existing)
			return 0
		}
		choice, err := chooseDuplicate(existing, incoming)
		if err != nil {
			log.Printf("report install error: %v", err)
			return 1
		}
		if choice == duplicateKeep {
			printTarget(existing)
			return 0
		}
		next, err := reportcfg.Replace(cfg, existing.ID, endpoint, key, identity.InstallID)
		if err != nil {
			log.Printf("report install error: %v", err)
			return 1
		}
		if err := reportcfg.Save(path, next); err != nil {
			log.Printf("report config write error: %v", err)
			return 1
		}
		printTarget(targetByID(next, existing.ID))
		return 0
	}

	if urlTargetOK {
		next, err := reportcfg.Replace(cfg, urlTarget.ID, endpoint, key, identity.InstallID)
		if err != nil {
			log.Printf("report install error: %v", err)
			return 1
		}
		if err := reportcfg.Save(path, next); err != nil {
			log.Printf("report config write error: %v", err)
			return 1
		}
		printTarget(targetByID(next, urlTarget.ID))
		return 0
	}

	next, target, err := reportcfg.Add(cfg, endpoint, key, identity.InstallID)
	if err != nil {
		log.Printf("report install error: %v", err)
		return 1
	}
	if err := reportcfg.Save(path, next); err != nil {
		log.Printf("report config write error: %v", err)
		return 1
	}
	printTarget(target)
	return 0
}

func resolveReportIdentity(endpoint, key string, requireHTTPS bool) (push.Identity, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return push.FetchIdentity(ctx, reportcfg.Target{
		URL: strings.TrimSpace(endpoint),
		Key: strings.TrimSpace(key),
	}, requireHTTPS)
}

func printIdentityNotice(identity push.Identity) {
	if identity.Created {
		fmt.Printf("server install_id was missing; created %s\n", identity.InstallID)
	}
}

func duplicateTarget(cfg reportcfg.Config, installID string) (reportcfg.Target, bool) {
	installID = strings.TrimSpace(installID)
	if installID == "" {
		return reportcfg.Target{}, false
	}
	for _, target := range cfg.Targets {
		if strings.TrimSpace(target.ServerInstallID) == installID {
			return target, true
		}
	}
	return reportcfg.Target{}, false
}

func sameReportTarget(a, b reportcfg.Target) bool {
	return strings.TrimSpace(a.URL) == strings.TrimSpace(b.URL) &&
		strings.TrimSpace(a.Key) == strings.TrimSpace(b.Key) &&
		strings.TrimSpace(a.ServerInstallID) == strings.TrimSpace(b.ServerInstallID)
}

func targetByURLKey(cfg reportcfg.Config, endpoint, key string) (reportcfg.Target, bool) {
	endpoint = strings.TrimSpace(endpoint)
	key = strings.TrimSpace(key)
	for _, target := range cfg.Targets {
		if strings.TrimSpace(target.URL) == endpoint && strings.TrimSpace(target.Key) == key {
			return target, true
		}
	}
	return reportcfg.Target{}, false
}

func targetByID(cfg reportcfg.Config, id int) reportcfg.Target {
	for _, target := range cfg.Targets {
		if target.ID == id {
			return target
		}
	}
	return reportcfg.Target{}
}

func printTarget(target reportcfg.Target) {
	fmt.Printf("%d\t%s\t%s\n", target.ID, target.URL, target.ServerInstallID)
}

type duplicateChoice uint8

const (
	duplicateKeep duplicateChoice = iota + 1
	duplicateReplace
)

func chooseDuplicate(existing, incoming reportcfg.Target) (duplicateChoice, error) {
	in, out, closeFn, ok := promptStreams()
	if !ok {
		return 0, fmt.Errorf("server install_id %s is already registered as target %d; run interactively and choose keep or replace", incoming.ServerInstallID, existing.ID)
	}
	defer closeFn()

	fmt.Fprintln(out, "Report target duplicates an existing server install_id.")
	printTargetConfig(out, "Existing", existing)
	printTargetConfig(out, "Incoming", incoming)
	fmt.Fprintln(out, "Choose one:")
	fmt.Fprintln(out, "  1) Keep existing target")
	fmt.Fprintln(out, "  2) Replace existing target")

	reader := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "Enter 1 or 2: ")
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("read duplicate choice: %w", err)
		}
		switch strings.TrimSpace(line) {
		case "1":
			return duplicateKeep, nil
		case "2":
			return duplicateReplace, nil
		}
		if errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("duplicate choice is required")
		}
		fmt.Fprintln(out, "Please enter 1 or 2.")
	}
}

func printTargetConfig(out io.Writer, label string, target reportcfg.Target) {
	id := "new"
	if target.ID > 0 {
		id = strconv.Itoa(target.ID)
	}
	fmt.Fprintf(out, "%s: id=%s url=%s key=%s install_id=%s\n", label, id, target.URL, maskedKey(target.Key), target.ServerInstallID)
}

func maskedKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "(empty)"
	}
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

func promptStreams() (io.Reader, io.Writer, func(), bool) {
	if info, err := os.Stdin.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) != 0 {
		return os.Stdin, os.Stdout, func() {}, true
	}
	if runtime.GOOS == "windows" {
		return nil, nil, nil, false
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, false
	}
	return tty, tty, func() { _ = tty.Close() }, true
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  Serve:")
	fmt.Println("    ./node [--net iface1,iface2] [--debug]")
	fmt.Println("    ./node serve [listen_ip] [listen_port] [--net iface1,iface2] [--debug]")
	fmt.Println()
	fmt.Println("  Push:")
	fmt.Println("    ./node push [interval_seconds] [--net iface1,iface2] [--debug] [--require-https]")
	fmt.Println()
	fmt.Println("  Report targets:")
	fmt.Println("    ./node report install <url> <key> [--require-https]")
	fmt.Println("    ./node report remove <id>")
	fmt.Println("    ./node report update <id> <key>")
	fmt.Println("    ./node report list")
	fmt.Println()
	fmt.Println("  Version:")
	fmt.Println("    ./node --version")
	fmt.Println("    ./node -v")
}

func printReportUsage() {
	fmt.Println("Usage:")
	fmt.Println("  ./node report install <url> <key> [--require-https]")
	fmt.Println("  ./node report remove <id>")
	fmt.Println("  ./node report update <id> <key>")
	fmt.Println("  ./node report list")
}
