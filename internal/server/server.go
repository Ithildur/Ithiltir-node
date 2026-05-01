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

//go:embed servepage/page.html servepage/assets/*
var servePageFiles embed.FS

const servePageDirEnv = "ITHILTIR_NODE_SERVE_PAGE_DIR"

type servePage struct {
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

func defaultServePage() fs.FS {
	return mustSubFS(servePageFiles, "servepage")
}

func defaultPage() servePage {
	return servePage{
		root:   defaultServePage(),
		assets: mustSubFS(servePageFiles, "servepage/assets"),
	}
}

func dirPage(dir string) servePage {
	return servePage{
		root:   os.DirFS(dir),
		assets: os.DirFS(filepath.Join(dir, "assets")),
	}
}

func resolveServePage() servePage {
	if dir := os.Getenv(servePageDirEnv); dir != "" {
		page := dirPage(dir)
		if ok := hasServePage(page.root); ok {
			return page
		}
		log.Printf("%s=%s does not contain page.html; using embedded serve page", servePageDirEnv, dir)
		return defaultPage()
	}

	exe, err := os.Executable()
	if err != nil {
		return defaultPage()
	}
	dir := filepath.Join(filepath.Dir(exe), "servepage")
	page := dirPage(dir)
	if ok := hasServePage(page.root); ok {
		return page
	}

	return defaultPage()
}

func hasServePage(fsys fs.FS) bool {
	info, err := fs.Stat(fsys, "page.html")
	return err == nil && !info.IsDir()
}

func serveHandler(fsys fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowGET(w, r) {
			return
		}
		if r.URL.Path != "/" && r.URL.Path != "/serve" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFileFS(w, r, fsys, "page.html")
	}
}

func assetHandler(fsys fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowGET(w, r) {
			return
		}
		name := r.URL.Path[len("/serve-assets/"):]
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
	servePage := resolveServePage()
	pageHandler := serveHandler(servePage.root)
	mux.HandleFunc("/", pageHandler)
	mux.HandleFunc("/serve", exact("/serve", pageHandler))
	mux.HandleFunc("/serve-assets/", assetHandler(servePage.assets))
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
