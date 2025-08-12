package lattice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	wasmv1 "github.com/cosmonic-labs/runtime-operator/rpc/wasmcloud/runtime/v1"
	"go.wasmcloud.dev/x/wasmbus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const HEARTBEAT_SUBJECT = "runtime.operator.heartbeat.>"
const HOST_API_PREFIX = "runtime.host"

// catchall timeout for requests to the host API
const maxRequestTimeout = 1 * time.Minute

type Dispatch interface {
	// ClientFor returns a client for the given host ID.
	ClientFor(hostID string) (Client, error)
	// Heartbeats returns a channel of host IDs that have sent a heartbeat.
	Heartbeats() <-chan string

	// Start starts the dispatch. (controller-runtime Runnable)
	Start(ctx context.Context) error
}

type Client interface {
	Heartbeat(ctx context.Context) (*wasmv1.HostHeartbeat, error)

	StartComponent(ctx context.Context, req *wasmv1.ComponentStartRequest) (*wasmv1.ComponentStartResponse, error)
	StartProvider(ctx context.Context, req *wasmv1.ProviderStartRequest) (*wasmv1.ProviderStartResponse, error)

	WorkloadStatus(ctx context.Context, req *wasmv1.WorkloadStatusRequest) (*wasmv1.WorkloadStatusResponse, error)
	WorkloadStop(ctx context.Context, req *wasmv1.WorkloadStopRequest) (*wasmv1.WorkloadStopResponse, error)
}

var _ Dispatch = (*dispatchNats)(nil)

func NewDispatch(bus wasmbus.Bus, heartbeatTimeout time.Duration, heartbeatCallback func(hostID string)) Dispatch {
	return &dispatchNats{
		bus:               bus,
		heartbeatTimeout:  heartbeatTimeout,
		clients:           make(map[string]time.Time),
		heartbeatCallback: heartbeatCallback,
	}
}

type dispatchNats struct {
	heartbeatTimeout time.Duration
	bus              wasmbus.Bus
	mtx              sync.Mutex
	// last heartbeat for each host
	// this is cleaned up when a caller requests a client for a host that has not sent a heartbeat
	clients           map[string]time.Time
	heartbeatCallback func(hostID string)
}

func (d *dispatchNats) ClientFor(hostID string) (Client, error) {
	d.mtx.Lock()
	defer d.mtx.Unlock()

	lastHeartbeat, ok := d.clients[hostID]
	if !ok {
		return &natsClient{
			bus:    d.bus,
			prefix: fmt.Sprintf("%s.%s", HOST_API_PREFIX, hostID),
		}, nil
	}

	if time.Since(lastHeartbeat) > d.heartbeatTimeout {
		delete(d.clients, hostID)
	}

	return &natsClient{
		bus:    d.bus,
		prefix: fmt.Sprintf("%s.%s", HOST_API_PREFIX, hostID),
	}, nil
}

func (d *dispatchNats) Heartbeats() <-chan string {
	return nil
}

// Implements manager.Runnable from controller runtime
func (d *dispatchNats) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("dispatchNats")

	subscription, err := d.bus.Subscribe(
		HEARTBEAT_SUBJECT,
		100,
	)
	if err != nil {
		return err
	}

	go subscription.Handle(func(msg *wasmbus.Message) {
		// safe to assume there are at least 3 parts to the subject cause that's how we subscribed
		hostID := msg.SubjectParts()[3]
		// update the last heartbeat time
		d.mtx.Lock()
		d.clients[hostID] = time.Now()
		d.mtx.Unlock()

		// call the callback
		d.heartbeatCallback(hostID)
	})

	logger.Info("Subscribed to heartbeat messages")
	<-ctx.Done()

	return subscription.Drain()
}

var _ Client = (*natsClient)(nil)

type natsClient struct {
	bus    wasmbus.Bus
	prefix string
}

func (c *natsClient) StartComponent(ctx context.Context, req *wasmv1.ComponentStartRequest) (*wasmv1.ComponentStartResponse, error) {
	var resp wasmv1.ComponentStartResponse
	if err := roundtrip(ctx, c.bus, c.subject("component", "start"), req, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *natsClient) StartProvider(ctx context.Context, req *wasmv1.ProviderStartRequest) (*wasmv1.ProviderStartResponse, error) {
	var resp wasmv1.ProviderStartResponse
	if err := roundtrip(ctx, c.bus, c.subject("provider", "start"), req, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *natsClient) WorkloadStatus(ctx context.Context, req *wasmv1.WorkloadStatusRequest) (*wasmv1.WorkloadStatusResponse, error) {
	var resp wasmv1.WorkloadStatusResponse
	if err := roundtrip(ctx, c.bus, c.subject("workload", "status"), req, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *natsClient) WorkloadStop(ctx context.Context, req *wasmv1.WorkloadStopRequest) (*wasmv1.WorkloadStopResponse, error) {
	var resp wasmv1.WorkloadStopResponse
	if err := roundtrip(ctx, c.bus, c.subject("workload", "stop"), req, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (c *natsClient) Heartbeat(ctx context.Context) (*wasmv1.HostHeartbeat, error) {
	var resp wasmv1.HostHeartbeat

	msg := wasmbus.NewMessage(c.subject("heartbeat"))

	reply, err := c.bus.Request(ctx, msg)
	if err != nil {
		return nil, err
	}

	return &resp, protojson.Unmarshal(reply.Data, &resp)
}

func (c *natsClient) subject(parts ...string) string {
	return strings.Join(append([]string{c.prefix}, parts...), ".")
}

func roundtrip[Req proto.Message, Resp proto.Message](ctx context.Context, bus wasmbus.Bus, subject string, req Req, resp Resp) error {
	ctx, cancel := context.WithTimeout(ctx, maxRequestTimeout)
	defer cancel()

	json, err := protojson.Marshal(req)
	if err != nil {
		return err
	}

	msg := wasmbus.NewMessage(subject)
	msg.Data = json

	reply, err := bus.Request(ctx, msg)
	if err != nil {
		return err
	}

	return protojson.Unmarshal(reply.Data, resp)
}
