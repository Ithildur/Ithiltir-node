package virt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const SocketPath = "/run/ithiltir-node/pve.sock"

var ErrBusy = errors.New("busy")
var ErrUnsupported = errors.New("unsupported")
var ErrSource = errors.New("source_unavailable")
var ErrVMNotFound = errors.New("vm_not_found")

type Capabilities struct {
	Version string `json:"version"`
	History bool   `json:"history"`
}

type Client struct {
	history *http.Client
	probe   *http.Client
}

func NewClient(path string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", path)
		},
		MaxConnsPerHost: 4, MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Minute,
	}
	// Capability checks must not queue behind all four history queries.
	probe := transport.Clone()
	probe.MaxConnsPerHost, probe.MaxIdleConnsPerHost = 1, 1
	return &Client{
		history: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		probe:   &http.Client{Transport: probe, Timeout: time.Second},
	}
}
func (c *Client) Close() {
	c.history.CloseIdleConnections()
	c.probe.CloseIdleConnections()
}
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	raw, err := call(ctx, c.probe, "GET", "/capabilities", nil)
	if err != nil {
		return Capabilities{}, err
	}
	var caps Capabilities
	err = json.Unmarshal(raw, &caps)
	return caps, err
}
func (c *Client) History(ctx context.Context, q HistoryQuery) ([]byte, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	return call(ctx, c.history, "POST", "/history", body)
}
func call(ctx context.Context, client *http.Client, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://pve"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests:
		return nil, ErrBusy
	case http.StatusNotImplemented:
		return nil, ErrUnsupported
	case http.StatusNotFound:
		return nil, ErrVMNotFound
	case http.StatusGatewayTimeout:
		return nil, context.DeadlineExceeded
	default:
		return nil, ErrSource
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, HistoryMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > HistoryMaxBytes {
		return nil, fmt.Errorf("PVE response exceeds limit")
	}
	return raw, nil
}
