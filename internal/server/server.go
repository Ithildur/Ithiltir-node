package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/nodeiface"
)

//go:embed localpage/page.html localpage/assets/*
var localPageFiles embed.FS

const localPageDirEnv = "ITHILTIR_NODE_LOCAL_PAGE_DIR"

type localPage struct {
	root   fs.FS
	assets fs.FS
}

func metricsHandler(s nodeiface.ReportSource, debug bool) http.HandlerFunc {
	return reportHandler(func() *metrics.NodeReport {
		m := s.Snapshot()
		if m == nil {
			return nil
		}
		report := metrics.NewNodeReport(s.Version(), s.Hostname(), s.Time(), m)
		return &report
	}, debug)
}

func reportHandler(reportFn func() *metrics.NodeReport, debug bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowGET(w, r) {
			return
		}
		report := reportFn()
		if report == nil {
			http.Error(w, "metrics not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		if debug {
			enc.SetIndent("", "  ")
		}
		_ = enc.Encode(report)
	}
}

func staticHandler(source nodeiface.StaticSource, debug bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowGET(w, r) {
			return
		}
		if source == nil {
			http.Error(w, "static not ready", http.StatusServiceUnavailable)
			return
		}
		static := source.Static()
		if static == nil {
			http.Error(w, "static not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		if debug {
			enc.SetIndent("", "  ")
		}
		_ = enc.Encode(static)
	}
}

func exact(path string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

func allowGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func defaultLocalPage() fs.FS {
	return mustSubFS(localPageFiles, "localpage")
}

func defaultPage() localPage {
	return localPage{
		root:   defaultLocalPage(),
		assets: mustSubFS(localPageFiles, "localpage/assets"),
	}
}

func dirPage(dir string) localPage {
	return localPage{
		root:   os.DirFS(dir),
		assets: os.DirFS(filepath.Join(dir, "assets")),
	}
}

func resolveLocalPage() localPage {
	if dir := os.Getenv(localPageDirEnv); dir != "" {
		page := dirPage(dir)
		if ok := hasLocalPage(page.root); ok {
			return page
		}
		log.Printf("%s=%s does not contain page.html; using embedded local page", localPageDirEnv, dir)
		return defaultPage()
	}

	exe, err := os.Executable()
	if err != nil {
		return defaultPage()
	}
	exePageDir := filepath.Join(filepath.Dir(exe), "localpage")
	page := dirPage(exePageDir)
	if ok := hasLocalPage(page.root); ok {
		return page
	}

	return defaultPage()
}

func hasLocalPage(fsys fs.FS) bool {
	info, err := fs.Stat(fsys, "page.html")
	return err == nil && !info.IsDir()
}

func localHandler(fsys fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowGET(w, r) {
			return
		}
		if r.URL.Path != "/" && r.URL.Path != "/local" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFileFS(w, r, fsys, "page.html")
	}
}

func assetHandler(prefix string, fsys fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowGET(w, r) {
			return
		}
		name := r.URL.Path[len(prefix):]
		if name == "" || name == "." || !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		info, err := fs.Stat(fsys, name)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, fsys, name)
	}
}

func NewServer(listenIP, listenPort string, s nodeiface.ReportSource, debug bool) (*http.Server, string) {
	mux := http.NewServeMux()
	localPage := resolveLocalPage()
	pageHandler := localHandler(localPage.root)
	mux.HandleFunc("/", pageHandler)
	mux.HandleFunc("/local", exact("/local", pageHandler))
	mux.HandleFunc("/local-assets/", assetHandler("/local-assets/", localPage.assets))
	mux.HandleFunc("/metrics", exact("/metrics", metricsHandler(s, debug)))
	staticSource, _ := s.(nodeiface.StaticSource)
	mux.HandleFunc("/static", exact("/static", staticHandler(staticSource, debug)))

	addr := net.JoinHostPort(listenIP, listenPort)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return srv, addr
}

func NewPushServer(listenIP, listenPort string, reportFn func() *metrics.NodeReport) (*http.Server, string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", exact("/", reportHandler(reportFn, true)))

	addr := net.JoinHostPort(listenIP, listenPort)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return srv, addr
}
