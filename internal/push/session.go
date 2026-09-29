package push

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"Ithiltir-node/internal/nodewire"
	"Ithiltir-node/internal/virt"
)

func (a *agent) startSessions(ctx context.Context) *sync.WaitGroup {
	wg := new(sync.WaitGroup)
	for _, target := range a.targets {
		wg.Go(func() {
			helper := virt.NewClient(virt.SocketPath)
			defer helper.Close()
			delay := time.Second
			for ctx.Err() == nil {
				started := time.Now()
				err := target.querySession(ctx, helper, a.source.Version())
				if err == nil || ctx.Err() != nil {
					return
				}
				if status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied {
					log.Printf("node query session target=%d credentials rejected", target.id)
					return
				}
				log.Printf("node query session target=%d: %v", target.id, err)
				if time.Since(started) > time.Minute {
					delay = time.Second
				}
				timer := time.NewTimer(delay + time.Duration(rand.Int64N(max(1, int64(delay/4)))))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				delay = min(delay*2, time.Minute)
			}
		})
	}
	return wg
}

func (t *target) querySession(parent context.Context, helper *virt.Client, version string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	client, err := t.rpcClient(ctx)
	if err != nil {
		return err
	}
	if client == nil {
		return nil
	}
	stream, err := client.Connect(rpcContext(ctx, t.secret))
	if err != nil {
		return err
	}
	caps, _ := helper.Capabilities(ctx)
	if err := stream.Send(&nodewire.NodeMessage{Body: &nodewire.NodeMessage_Capabilities{Capabilities: &nodewire.Capabilities{Version: version, PveHistory: caps.History, HelperVersion: caps.Version}}}); err != nil {
		return err
	}
	incoming := make(chan *nodewire.DashMessage, 1)
	outgoing := make(chan *nodewire.NodeMessage, 64)
	failures := make(chan error, 2)
	var tasks sync.WaitGroup
	tasks.Go(func() {
		for {
			message, err := stream.Recv()
			if err != nil {
				failures <- err
				return
			}
			select {
			case incoming <- message:
			case <-ctx.Done():
				return
			}
		}
	})
	tasks.Go(func() {
		for {
			select {
			case message := <-outgoing:
				if err := stream.Send(message); err != nil {
					failures <- err
					return
				}
			case <-ctx.Done():
				return
			}
		}
	})
	// Only this loop owns pending. The helper client's connection pool bounds
	// execution; each admitted query keeps its deadline while waiting for a slot.
	pending := make(map[string]context.CancelFunc)
	completed := make(chan *nodewire.NodeMessage, 32)
	send := func(message *nodewire.NodeMessage) error {
		select {
		case outgoing <- message:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			return errors.New("query session send queue full")
		}
	}
	defer func() {
		cancel()
		tasks.Wait()
		_ = stream.CloseSend()
	}()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	check := time.NewTicker(30 * time.Second)
	defer check.Stop()
	lastSeen := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case result := <-completed:
			pending[result.Id]()
			delete(pending, result.Id)
			if err := send(result); err != nil {
				return err
			}
		case message := <-incoming:
			lastSeen = time.Now()
			if message.GetCancel() {
				stop := pending[message.Id]
				if stop != nil {
					stop()
				}
				continue
			}
			query := message.GetHistory()
			if query == nil {
				continue
			}
			if message.Id == "" || len(message.Id) > 64 {
				return errors.New("invalid query id")
			}
			q := virt.HistoryQuery{VMID: int(query.VmId), Timeframe: query.Timeframe, Consolidation: query.Consolidation}
			if q.Validate() != nil || query.TimeoutMs == 0 {
				return errors.New("invalid history query")
			}
			if _, exists := pending[message.Id]; exists {
				return errors.New("duplicate query id")
			}
			if len(pending) >= 32 {
				if err := send(&nodewire.NodeMessage{Id: message.Id, Body: &nodewire.NodeMessage_Result{Result: &nodewire.QueryResult{Error: "busy"}}}); err != nil {
					return err
				}
				continue
			}
			jobCtx, stop := context.WithTimeout(ctx, min(10*time.Second, time.Duration(query.TimeoutMs)*time.Millisecond))
			pending[message.Id] = stop
			tasks.Go(func() {
				result := historyResult(jobCtx, helper, message.Id, q)
				select {
				case completed <- result:
				case <-ctx.Done():
				}
			})
		case <-heartbeat.C:
			if time.Since(lastSeen) > 60*time.Second {
				return errors.New("query session heartbeat timeout")
			}
			if err := send(&nodewire.NodeMessage{Body: &nodewire.NodeMessage_Heartbeat{Heartbeat: true}}); err != nil {
				return err
			}
		case <-check.C:
			current, err := helper.Capabilities(ctx)
			if err != nil {
				continue
			}
			if current.History != caps.History || current.Version != caps.Version {
				return errors.New("PVE capabilities changed")
			}
		}
	}
}

func historyResult(ctx context.Context, helper *virt.Client, id string, query virt.HistoryQuery) *nodewire.NodeMessage {
	raw, err := helper.History(ctx, query)
	code := ""
	switch {
	case err == nil:
	case errors.Is(err, virt.ErrBusy):
		code = "busy"
	case errors.Is(err, virt.ErrUnsupported):
		code = "unsupported"
	case errors.Is(err, virt.ErrVMNotFound):
		code = "vm_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	default:
		code = "source_unavailable"
	}
	return &nodewire.NodeMessage{Id: id, Body: &nodewire.NodeMessage_Result{Result: &nodewire.QueryResult{Json: raw, Error: code}}}
}
