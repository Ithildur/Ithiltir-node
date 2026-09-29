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

	"Ithiltir-node/internal/nodewire"
	"Ithiltir-node/internal/virt"
)

// startVirt owns independent delivery loops; no VM operation runs in the host
// metrics loop. Each target keeps only its latest successful delivery marker.
func startVirt(ctx context.Context, targets []*target) *sync.WaitGroup {
	wg := new(sync.WaitGroup)
	path := strings.TrimSpace(os.Getenv("ITHILTIR_NODE_VIRT_CACHE"))
	if path == "" {
		return wg
	}
	for _, t := range targets {
		if !strings.HasSuffix(t.endpoint.Path, "/metrics") {
			log.Printf("virt target=%d requires a /metrics report URL", t.id)
			continue
		}
		wg.Go(func() { pushVirt(ctx, t, path) })
	}
	return wg
}

func pushVirt(ctx context.Context, target *target, path string) {
	var sent time.Time
	var snapshot virt.Snapshot
	var readError string
	delay := 5 * time.Second
	limiter := logLimiter{cooldown: time.Minute}
	for ctx.Err() == nil {
		current, err := virt.Read(path)
		if err == nil {
			snapshot, readError = current, ""
		} else {
			message := virt.ErrorText(fmt.Errorf("cache unavailable: %w", err))
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
				err = sendVirt(ctx, target, body)
			}
			if err != nil {
				limiter.logf("virt push target=%d: %v", target.id, err)
				delay = min(delay*2, time.Minute)
			} else {
				sent, delay = snapshot.CollectedAt, 5*time.Second
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func sendVirt(ctx context.Context, target *target, body []byte) error {
	client, err := target.rpcClient(ctx)
	if err != nil {
		return err
	}
	if client != nil {
		callCtx, cancel := rpcTimeout(ctx, target.secret)
		defer cancel()
		_, err := client.Virt(callCtx, &nodewire.Report{Json: body})
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
	client := *target.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(req)
}
