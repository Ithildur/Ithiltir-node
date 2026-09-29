//go:build linux

package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"Ithiltir-node/internal/virt"
)

type peerKey struct{}

func Serve(ctx context.Context, host, output, socket, dir string, gid int, version string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := guestLock(filepath.Join(dir, "service.lock"))
	if err != nil {
		return err
	}
	if lock == nil {
		return errors.New("PVE service already running")
	}
	defer lock.Close()
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("PVE socket path is not a socket")
		}
		if err := os.Remove(socket); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chown(socket, 0, gid); err != nil {
		return err
	}
	if err := os.Chmod(socket, 0660); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cache := newHistoryCache(runCtx, host, output, dir)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /capabilities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(virt.Capabilities{Version: version, History: true})
	})
	mux.HandleFunc("POST /history", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		defer r.Body.Close()
		var q virt.HistoryQuery
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&q); err != nil || q.Validate() != nil {
			http.Error(w, "invalid_query", 400)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid_query", 400)
			return
		}
		raw, err := cache.get(r.Context(), q)
		if err != nil {
			status := http.StatusBadGateway
			switch {
			case errors.Is(err, virt.ErrBusy):
				status = http.StatusTooManyRequests
			case errors.Is(err, virt.ErrVMNotFound):
				status = http.StatusNotFound
			case errors.Is(err, context.DeadlineExceeded):
				status = http.StatusGatewayTimeout
			case errors.Is(err, context.Canceled):
				return
			}
			log.Printf("PVE history VM %d: %v", q.VMID, err)
			http.Error(w, http.StatusText(status), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowed, _ := r.Context().Value(peerKey{}).(bool); !allowed {
				http.Error(w, "forbidden", 403)
				return
			}
			mux.ServeHTTP(w, r)
		}),
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, peerKey{}, allowedPeer(conn, gid))
		},
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: time.Minute,
		BaseContext: func(net.Listener) context.Context { return runCtx },
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		repeat(runCtx, 0, 30*time.Second, func(ctx context.Context) error { return Refresh(ctx, host, output, dir, gid) })
	})
	wg.Go(func() {
		repeat(runCtx, 45*time.Second, time.Minute, func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
			defer cancel()
			snapshot, err := virt.Read(output)
			if err != nil {
				return err
			}
			return collectGuests(ctx, dir, snapshot, cache.guest)
		})
	})
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	var serveErr error
	select {
	case serveErr = <-errCh:
	case <-ctx.Done():
	}
	cancel()
	stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := server.Shutdown(stopCtx)
	stop()
	if shutdownErr != nil {
		_ = server.Close()
	}
	wg.Wait()
	cache.wg.Wait()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return shutdownErr
}

func allowedPeer(conn net.Conn, gid int) bool {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return false
	}
	var credential *syscall.Ucred
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credential, credentialErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return false
	}
	return credentialErr == nil && credential != nil && (credential.Uid == 0 || credential.Gid == uint32(gid))
}
func repeat(ctx context.Context, initial, interval time.Duration, run func(context.Context) error) {
	delay := initial
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := run(ctx); err != nil && ctx.Err() == nil {
			log.Print(fmt.Errorf("PVE cache: %w", err))
		}
		delay = interval
	}
}
