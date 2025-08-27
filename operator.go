package runtime_operator

import (
	"time"

	runtimecontroller "github.com/cosmonic-labs/runtime-operator/internal/controller/runtime"
	"github.com/cosmonic-labs/runtime-operator/pkg/lattice"
	"github.com/cosmonic-labs/runtime-operator/pkg/wasmbus"
	"github.com/nats-io/nats.go"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type EmbeddedOperatorConfig struct {
	// NATS connection string. Used to communicate with hosts.
	NatsURL string
	// NATS options. Used to configure the NATS connection.
	NatsOptions []nats.Option
	// Heartbeat TTL. Used to determine how long to wait before considering a host unreachable.
	HeartbeatTTL time.Duration
	// Host CPU threshold (percentage).
	// Used to calculate workload scheduling, avoiding hosts that are over this threshold.
	HostCPUThreshold float64
	// Host Memory threshold (percentage).
	// Used to calculate workload scheduling, avoiding hosts that are over this threshold.
	HostMemoryThreshold float64
	// Disable Artifact Controller. If set, Artifacts must be marked as 'Ready' elsewhere.
	// Useful when introducing a custom artifact management solution.
	DisableArtifactController bool
}

// EmbeddedOperator is the main struct for the embedded operator.
// It allows embedding the Runtime Operator into other applications.
type EmbeddedOperator struct {
	Bus      wasmbus.Bus
	Dispatch lattice.Dispatch
}

// NewEmbeddedOperator creates a new EmbeddedOperator.
func NewEmbeddedOperator(mgr manager.Manager, cfg EmbeddedOperatorConfig) (*EmbeddedOperator, error) {
	nc, err := wasmbus.NatsConnect(cfg.NatsURL, cfg.NatsOptions...)
	if err != nil {
		return nil, err
	}
	bus := wasmbus.NewNatsBus(nc)

	hostHeartbeats := make(chan string, 100)
	dispatch := lattice.NewDispatch(bus, cfg.HeartbeatTTL, func(hostID string) {
		select {
		case hostHeartbeats <- hostID:
		default:
		}
	})

	if mgr.Add(dispatch) != nil {
		return nil, err
	}

	crdIndexer := &lattice.Indexer{
		Client:       mgr.GetClient(),
		FieldIndexer: mgr.GetFieldIndexer(),
	}
	if err := mgr.Add(crdIndexer); err != nil {
		return nil, err
	}

	if err = (&runtimecontroller.HostReconciler{
		Client:                      mgr.GetClient(),
		Scheme:                      mgr.GetScheme(),
		Dispatch:                    dispatch,
		HostHeartbeats:              hostHeartbeats,
		CPUBackpressureThreshold:    cfg.HostCPUThreshold,
		MemoryBackpressureThreshold: cfg.HostMemoryThreshold,
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	if err = (&runtimecontroller.ConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	if err = (&runtimecontroller.ComponentReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Dispatch: dispatch,
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	if err = (&runtimecontroller.ComponentReplicaReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Dispatch: dispatch,
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	if err = (&runtimecontroller.ProviderReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Dispatch: dispatch,
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	if err = (&runtimecontroller.ProviderReplicaReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Dispatch: dispatch,
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	if err = (&runtimecontroller.LinkReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	if !cfg.DisableArtifactController {
		if err = (&runtimecontroller.ArtifactReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			return nil, err
		}
	}

	return &EmbeddedOperator{
		Dispatch: dispatch,
		Bus:      bus,
	}, nil
}
