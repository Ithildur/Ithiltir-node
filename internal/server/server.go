package server

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/nodeiface"
)

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

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	_, _ = w.Write([]byte("Ithiltir-node is running. GET /metrics for details.\n"))
}

func NewServer(listenIP, listenPort string, s nodeiface.ReportSource, debug bool) (*http.Server, string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", exact("/", rootHandler))
	mux.HandleFunc("/metrics", exact("/metrics", metricsHandler(s, debug)))

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
