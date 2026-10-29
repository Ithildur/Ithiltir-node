//go:build linux

package pve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"strconv"
	"time"

	"Ithiltir-node/internal/virt"
)

// Connections belong to one observation and are closed promptly: PVE's own
// monitor clients need access to these same single-client sockets.
type monitor struct {
	conn     net.Conn
	reader   *bufio.Reader
	stop     func() bool
	guest    bool
	sequence int
}

type monitorError struct {
	Class       string `json:"class"`
	Description string `json:"desc"`
}

func (e *monitorError) Error() string { return e.Class + ": " + e.Description }

type monitorReply struct {
	Greeting json.RawMessage `json:"QMP"`
	ID       string          `json:"id"`
	Result   json.RawMessage `json:"return"`
	Error    *monitorError   `json:"error"`
}

func openMonitor(ctx context.Context, path string, guest bool) (*monitor, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	m := &monitor{conn: conn, reader: bufio.NewReader(conn), guest: guest}
	m.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Second)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		m.Close()
		return nil, err
	}
	if guest {
		err = m.syncGuest()
	} else {
		var reply monitorReply
		reply, err = m.read()
		if err == nil && len(reply.Greeting) == 0 {
			err = errors.New("missing QMP greeting")
		}
		if err == nil {
			_, err = m.call("qmp_capabilities")
		}
	}
	if err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

func (m *monitor) Close() { m.stop(); _ = m.conn.Close() }

func (m *monitor) read() (monitorReply, error) {
	var line []byte
	for {
		part, err := m.reader.ReadSlice('\n')
		if len(line)+len(part) > virt.MaxBytes {
			return monitorReply{}, errors.New("monitor response exceeds limit")
		}
		line = append(line, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return monitorReply{}, err
		}
		var reply monitorReply
		err = json.Unmarshal(line, &reply)
		return reply, err
	}
}

func (m *monitor) syncGuest() error {
	// Flush a partial request left by a disconnected client. The matching
	// delimited response discards stale replies before reading our IP result.
	id := rand.Uint64() >> 1
	raw, err := json.Marshal(map[string]any{"execute": "guest-sync-delimited", "arguments": map[string]uint64{"id": id}})
	if err != nil {
		return err
	}
	if _, err := m.conn.Write(append(append([]byte{0xff}, raw...), '\n')); err != nil {
		return err
	}
	for discarded := 0; discarded < virt.MaxBytes; discarded++ {
		b, err := m.reader.ReadByte()
		if err != nil {
			return err
		}
		if b != 0xff {
			continue
		}
		reply, err := m.read()
		if err != nil {
			return err
		}
		var received uint64
		if json.Unmarshal(reply.Result, &received) == nil && received == id {
			return nil
		}
	}
	return errors.New("guest synchronization exceeds limit")
}

func (m *monitor) call(command string) (json.RawMessage, error) {
	m.sequence++
	id := strconv.Itoa(m.sequence)
	request := map[string]any{"execute": command}
	if !m.guest {
		request["id"] = id
	}
	if err := json.NewEncoder(m.conn).Encode(request); err != nil {
		return nil, err
	}
	for {
		reply, err := m.read()
		if err != nil {
			return nil, err
		}
		if !m.guest && reply.ID != id {
			continue
		} // asynchronous QMP events
		if reply.Error != nil {
			return nil, reply.Error
		}
		if len(reply.Result) == 0 || bytes.Equal(reply.Result, []byte("null")) {
			return nil, io.ErrUnexpectedEOF
		}
		return reply.Result, nil
	}
}

func collectMonitor(ctx context.Context, vm *virt.VM) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	m, err := openMonitor(ctx, fmt.Sprintf("/run/qemu-server/%d.qmp", vm.ID), false)
	if err != nil {
		return err
	}
	defer m.Close()
	raw, err := m.call("query-status")
	if err != nil {
		return err
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return err
	}
	if status.Status == "" {
		return errors.New("missing QEMU status")
	}
	vm.QMPStatus = status.Status
	if status.Status != "running" {
		return nil
	}
	if memory, err := memoryUsage(vm.ID); err == nil {
		vm.Memory = &memory
	} else {
		log.Printf("PVE VM %d memory: %v", vm.ID, err)
	}
	raw, err = m.call("query-blockstats")
	if err != nil {
		return err
	}
	var disks []struct {
		Stats struct {
			Read  *int64 `json:"rd_bytes"`
			Write *int64 `json:"wr_bytes"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(raw, &disks); err != nil {
		return err
	}
	var read, written int64
	for _, disk := range disks {
		if disk.Stats.Read == nil || disk.Stats.Write == nil || *disk.Stats.Read < 0 || *disk.Stats.Write < 0 {
			return errors.New("invalid QEMU disk counters")
		}
		read += *disk.Stats.Read
		written += *disk.Stats.Write
	}
	vm.DiskRead, vm.DiskWrite = &read, &written
	raw, err = m.call("query-balloon")
	if reply, ok := errors.AsType[*monitorError](err); ok && reply.Class == "DeviceNotActive" {
		return nil
	} // no active balloon: retain cgroup memory
	if err != nil {
		return err
	}
	var balloon struct {
		Maximum *int64 `json:"max_mem"`
		Total   *int64 `json:"total_mem"`
		Free    *int64 `json:"free_mem"`
	}
	if err := json.Unmarshal(raw, &balloon); err != nil {
		return err
	}
	if balloon.Maximum != nil && *balloon.Maximum > 0 {
		vm.MemoryLimit = balloon.Maximum
		if balloon.Total != nil && balloon.Free != nil && *balloon.Free >= 0 && *balloon.Total >= *balloon.Free {
			vm.Memory = new(*balloon.Total - *balloon.Free)
		}
	}
	return nil
}
