//go:build linux

package pve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestMonitorProtocolAndCancellation(t *testing.T) {
	for _, guest := range []bool{false, true} {
		t.Run(fmt.Sprintf("guest=%t", guest), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "monitor")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				if guest {
					b, err := reader.ReadByte()
					if err != nil || b != 0xff {
						done <- fmt.Errorf("missing guest flush: %x, %v", b, err)
						return
					}
					line, err := reader.ReadBytes('\n')
					if err != nil {
						done <- err
						return
					}
					var sync struct {
						Arguments struct {
							ID uint64 `json:"id"`
						} `json:"arguments"`
					}
					if err := json.Unmarshal(line, &sync); err != nil {
						done <- err
						return
					}
					// A disconnected reader left both partial JSON and an old sync reply.
					_, _ = fmt.Fprintf(conn, "broken response\xff{\"return\":0}\n\xff{\"return\":%d}\n", sync.Arguments.ID)
				} else {
					_, _ = io.WriteString(conn, "{\"QMP\":{\"version\":{}}}\n")
					line, err := reader.ReadBytes('\n')
					if err != nil {
						done <- err
						return
					}
					var request map[string]string
					if err := json.Unmarshal(line, &request); err != nil || request["execute"] != "qmp_capabilities" {
						done <- fmt.Errorf("capabilities: %s, %v", line, err)
						return
					}
					_, _ = fmt.Fprintf(conn, "{\"return\":{},\"id\":%q}\n", request["id"])
				}
				line, err := reader.ReadBytes('\n')
				if err != nil {
					done <- err
					return
				}
				var request map[string]string
				if err := json.Unmarshal(line, &request); err != nil {
					done <- err
					return
				}
				if guest {
					// Fragment a QGA response across writes.
					_, _ = io.WriteString(conn, "{\"return\":[{\"ip-addresses\":[")
					_, _ = io.WriteString(conn, "{\"ip-address\":\"192.0.2.1\"}]}]}\n")
				} else {
					_, _ = io.WriteString(conn, "{\"event\":\"STOP\",\"data\":{}}\n")
					_, _ = fmt.Fprintf(conn, "{\"return\":{\"status\":\"paused\"},\"id\":%q}\n", request["id"])
				}
				// The next request intentionally stalls; cancellation must close it.
				if _, err := reader.ReadBytes('\n'); err != nil {
					done <- err
					return
				}
				_, err = reader.ReadByte()
				if err != io.EOF {
					done <- fmt.Errorf("connection survived cancellation: %v", err)
					return
				}
				done <- nil
			}()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			m, err := openMonitor(ctx, path, guest)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			command := "query-status"
			if guest {
				command = "guest-network-get-interfaces"
			}
			raw, err := m.call(command)
			if err != nil {
				t.Fatal(err)
			}
			if guest {
				ips, err := decodeIPs(raw)
				if err != nil || len(ips) != 1 || ips[0] != "192.0.2.1" {
					t.Fatalf("guest result: %s, %v", raw, err)
				}
			} else if string(raw) != `{"status":"paused"}` {
				t.Fatalf("QMP result: %s", raw)
			}
			time.AfterFunc(20*time.Millisecond, cancel)
			if _, err := m.call(command); err == nil {
				t.Fatal("stalled monitor ignored cancellation")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("server connection did not close")
			}
		})
	}
}
