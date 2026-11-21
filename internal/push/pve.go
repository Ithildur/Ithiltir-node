package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	"Ithiltir-node/internal/nodewire"
	"Ithiltir-node/internal/virt"
)

type pveTarget struct {
	target *target
	mu     sync.Mutex
	conn   *grpc.ClientConn
}

func startPVE(ctx context.Context, targets []*target, version string) func() {
	var wg sync.WaitGroup
	path := strings.TrimSpace(os.Getenv("ITHILTIR_NODE_PVE_SOCKET"))
	if path == "" {
		return wg.Wait
	}
	for _, t := range targets {
		wg.Go(func() {
			pve := &pveTarget{target: t}
			defer pve.close()
			var delivery sync.WaitGroup
			if t.staticURL() != "" {
				delivery.Go(func() { pve.pushVirt(ctx, path) })
			} else {
				log.Printf("virt target=%d requires a /metrics report URL", t.id)
			}
			pve.runSession(ctx, path, version)
			delivery.Wait()
		})
	}
	return wg.Wait
}

func (p *pveTarget) rpcClient(ctx context.Context) (nodewire.NodeClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		t := p.target
		t.rpc.mu.Lock()
		enabled, err := t.selectRPC(ctx)
		t.rpc.mu.Unlock()
		if err != nil || !enabled {
			return nil, err
		}
		p.conn, err = t.newRPCConn()
		if err != nil {
			return nil, err
		}
	}
	return nodewire.NewNodeClient(p.conn), nil
}

func (p *pveTarget) close() {
	if p.conn != nil {
		p.conn.Close()
	}
}

func (p *pveTarget) pushVirt(ctx context.Context, path string) {
	helper := virt.NewClient(path)
	defer helper.Close()
	var sent time.Time
	var snapshot virt.Snapshot
	var readError string
	delay := 3 * time.Second
	limiter := logLimiter{cooldown: time.Minute}
	for ctx.Err() == nil {
		started := time.Now()
		current, err := helper.Snapshot(ctx)
		if err == nil {
			snapshot, readError = current, ""
		} else {
			message := virt.ErrorText(fmt.Errorf("PVE helper unavailable: %w", err))
			if readError != message {
				readError = message
				snapshot.Schema, snapshot.Provider, snapshot.TTLSeconds = 1, "pve", 90
				snapshot.CollectedAt, snapshot.Status, snapshot.Error = time.Now().UTC(), "error", message
			}
		}
		if !snapshot.CollectedAt.Equal(sent) {
			body, marshalErr := json.Marshal(snapshot)
			err = marshalErr
			if err == nil {
				err = p.sendVirt(ctx, body)
			}
			if err != nil {
				limiter.logf("virt push target=%d: %v", p.target.id, err)
				delay = min(delay*2, time.Minute)
			} else {
				sent, delay = snapshot.CollectedAt, 3*time.Second
			}
		}
		timer := time.NewTimer(max(0, delay-time.Since(started)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *pveTarget) sendVirt(ctx context.Context, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := p.rpcClient(ctx)
	if err != nil {
		return err
	}
	target := p.target
	if client != nil {
		_, err := client.Virt(rpcContext(ctx, target.secret), &nodewire.Report{Json: body})
		return err
	}
	resp, err := sendVirtHTTP(ctx, target, body)
	if err != nil && target.fallbackHTTP(err) {
		resp, err = sendVirtHTTP(ctx, target, body)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func sendVirtHTTP(ctx context.Context, target *target, body []byte) (*http.Response, error) {
	target.mu.RLock()
	endpoint := *target.endpoint
	target.mu.RUnlock()
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/metrics") + "/virt"
	endpoint.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Secret", target.secret)
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client.Do(req)
}
