package runtime

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/wasmCloud/runtime-operator/api/condition"
	"github.com/wasmCloud/runtime-operator/pkg/wasmbus"

	runtimev1alpha1 "github.com/wasmCloud/runtime-operator/api/runtime/v1alpha1"
	runtimev2 "github.com/wasmCloud/runtime-operator/pkg/rpc/wasmcloud/runtime/v2"
)

const (
	workloadReconcileInterval     = 1 * time.Minute
	workloadFinalizerName         = "runtime.wasmcloud.dev/workload-finalizer"
	workloadSchedulableHostsIndex = "spec.isSchedulable"
)

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Bus    wasmbus.Bus

	reconciler condition.AnyConditionedReconciler
}

func (r *WorkloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconciler.Reconcile(ctx, req)
}

func (r *WorkloadReconciler) reconcileConfig(ctx context.Context, workload *runtimev1alpha1.Workload) error {
	// If the workload is already assigned to a host, there's nothing to do.
	// We only need the config during workload placement.
	if workload.Status.HostID != "" {
		return nil
	}

	if workload.Spec.Service != nil {
		if workload.Spec.Service.LocalResources.Environment != nil {
			_, err := MaterializeConfigLayer(ctx, r.Client, workload.Namespace, workload.Spec.Service.LocalResources.Environment)
			if err != nil {
				return fmt.Errorf("materializing local resources config: %w", err)
			}
		}
	}

	for _, c := range workload.Spec.Components {
		if c.LocalResources != nil {
			if c.LocalResources.Environment != nil {
				_, err := MaterializeConfigLayer(ctx, r.Client, workload.Namespace, c.LocalResources.Environment)
				if err != nil {
					return fmt.Errorf("materializing local resources config for component %q: %w", c.Name, err)
				}
			}
		}
	}

	return nil
}

func (r *WorkloadReconciler) reconcileHostSelection(ctx context.Context, workload *runtimev1alpha1.Workload) error {
	// If the workload is already assigned to a host, there's nothing to do.
	if workload.Status.HostID != "" {
		return nil
	}

	if !workload.Status.AllTrue(runtimev1alpha1.WorkloadConditionConfig) {
		return condition.ErrStatusUnknown(fmt.Errorf("waiting for config"))
	}

	condition.ForceStatusUpdate(ctx)
	if workload.Spec.HostID != "" {
		workload.Status.HostID = workload.Spec.HostID
		return condition.ErrSkipReconciliation()
	}

	selectedHost, err := r.findFreeHost(ctx, workload)
	if err != nil {
		return err
	}

	workload.Status.HostID = selectedHost
	return condition.ErrSkipReconciliation()
}

func (r *WorkloadReconciler) findFreeHost(ctx context.Context, workload *runtimev1alpha1.Workload) (string, error) {
	hostList := runtimev1alpha1.HostList{}

	if err := r.List(ctx, &hostList,
		client.MatchingLabels(workload.Spec.HostSelector),
		client.MatchingFields{
			workloadSchedulableHostsIndex: string(condition.ConditionTrue),
		}); err != nil {
		return "", err
	}

	// Shuffle the host list
	rand.Shuffle(len(hostList.Items), func(i, j int) {
		hostList.Items[i], hostList.Items[j] = hostList.Items[j], hostList.Items[i]
	})
	for _, host := range hostList.Items {
		if host.Status.IsAvailable() {
			return host.HostID, nil
		}
	}

	return "", fmt.Errorf("no suitable host found")
}

func (r *WorkloadReconciler) reconcilePlacement(ctx context.Context, workload *runtimev1alpha1.Workload) error {
	if workload.Status.HostID == "" {
		return condition.ErrStatusUnknown(fmt.Errorf("waiting for Host Selection"))
	}
	// don't replace
	if workload.Status.WorkloadID != "" {
		return nil
	}

	volumes := make([]*runtimev2.Volume, len(workload.Spec.Volumes))
	for _, v := range workload.Spec.Volumes {
		vol := &runtimev2.Volume{
			Name: v.Name,
		}
		switch {
		case v.EphemeralVolume != nil:
			vol.VolumeType = &runtimev2.Volume_EmptyDir{}
		case v.HostPathVolume != nil:
			vol.VolumeType = &runtimev2.Volume_HostPath{
				HostPath: &runtimev2.HostPathVolume{
					LocalPath: v.HostPathVolume.Path,
				},
			}
		}

		volumes = append(volumes, vol)
	}

	witWorld := &runtimev2.WitWorld{
		HostInterfaces: make([]*runtimev2.WitInterface, 0, len(workload.Spec.HostInterfaces)),
		Components:     make([]*runtimev2.Component, 0, len(workload.Spec.Components)),
	}
	for _, hi := range workload.Spec.HostInterfaces {
		hiConfig, err := MaterializeConfigLayer(ctx, r.Client, workload.Namespace, &hi.ConfigLayer)
		if err != nil {
			return fmt.Errorf("materializing host interface config for %s/%s: %w", hi.Namespace, hi.Package, err)
		}
		witWorld.HostInterfaces = append(witWorld.HostInterfaces, &runtimev2.WitInterface{
			Namespace:  hi.Namespace,
			Package:    hi.Package,
			Version:    hi.Version,
			Interfaces: hi.Interfaces,
			Config:     hiConfig,
		})
	}

	for _, c := range workload.Spec.Components {
		localResources := &runtimev2.LocalResources{}

		if c.LocalResources != nil {
			localResources.AllowedHosts = c.LocalResources.AllowedHosts
			localResources.Config = c.LocalResources.Config

			if c.LocalResources.Environment != nil {
				localEnvironment, err := MaterializeConfigLayer(ctx, r.Client, workload.Namespace, c.LocalResources.Environment)
				if err != nil {
					return fmt.Errorf("materializing local resources config for component %q: %w", c.Name, err)
				}
				localResources.Environment = localEnvironment
			}
		}

		witWorld.Components = append(witWorld.Components, &runtimev2.Component{
			Image:          c.Image,
			PoolSize:       c.PoolSize,
			MaxInvocations: c.MaxInvocations,
			LocalResources: localResources,
		})
	}

	var service *runtimev2.Service
	if s := workload.Spec.Service; s != nil {
		localResources := &runtimev2.LocalResources{}

		if s.LocalResources != nil {
			localResources.AllowedHosts = s.LocalResources.AllowedHosts
			localResources.Config = s.LocalResources.Config

			if s.LocalResources.Environment != nil {
				localEnvironment, err := MaterializeConfigLayer(ctx, r.Client, workload.Namespace, s.LocalResources.Environment)
				if err != nil {
					return fmt.Errorf("materializing local resources config for service: %w", err)
				}
				localResources.Environment = localEnvironment
			}
		}

		service = &runtimev2.Service{
			Image:          s.Image,
			LocalResources: localResources,
			MaxRestarts:    uint64(s.MaxRestarts),
		}
	}

	req := &runtimev2.WorkloadStartRequest{
		Workload: &runtimev2.Workload{
			Namespace:   workload.Namespace,
			Name:        workload.Name,
			Annotations: workload.GetAnnotations(),
			WitWorld:    witWorld,
			Volumes:     volumes,
			Service:     service,
		},
	}

	client := NewWorkloadClient(r.Bus, workload.Status.HostID)
	resp, err := client.Start(ctx, req)
	if err != nil {
		return err
	}

	// Set the WorkloadID in the status
	workload.Status.WorkloadID = resp.WorkloadStatus.WorkloadId
	condition.ForceStatusUpdate(ctx)
	return condition.ErrSkipReconciliation()
}

func (r *WorkloadReconciler) reconcileSync(ctx context.Context, workload *runtimev1alpha1.Workload) error {
	if !workload.Status.AllTrue(runtimev1alpha1.WorkloadConditionPlacement) {
		return condition.ErrStatusUnknown(fmt.Errorf("workload is not placed yet"))
	}

	client := NewWorkloadClient(r.Bus, workload.Status.HostID)
	req := &runtimev2.WorkloadStatusRequest{
		WorkloadId: workload.Status.WorkloadID,
	}

	resp, err := client.Status(ctx, req)
	if err != nil {
		return err
	}
	if resp.WorkloadStatus.WorkloadState == runtimev2.WorkloadState_WORKLOAD_STATE_RUNNING ||
		resp.WorkloadStatus.WorkloadState == runtimev2.WorkloadState_WORKLOAD_STATE_COMPLETED {
		return nil
	}

	return fmt.Errorf("workload is not operational: %s", resp.WorkloadStatus.WorkloadState.String())
}

func (r *WorkloadReconciler) reconcileReady(_ context.Context, workload *runtimev1alpha1.Workload) error {
	if !workload.Status.AllTrue(runtimev1alpha1.WorkloadConditionPlacement, runtimev1alpha1.WorkloadConditionSync) {
		return fmt.Errorf("workload is not placed or not synced")
	}

	return nil
}

func (r *WorkloadReconciler) finalize(ctx context.Context, workload *runtimev1alpha1.Workload) error {
	if !workload.Status.AllTrue(runtimev1alpha1.WorkloadConditionPlacement) {
		// nothing to do, the workload was never placed
		return nil
	}

	client := NewWorkloadClient(r.Bus, workload.Status.HostID)
	req := &runtimev2.WorkloadStopRequest{
		WorkloadId: workload.Status.WorkloadID,
	}

	_, err := client.Stop(ctx, req)
	if err != nil {
		logger := ctrl.LoggerFrom(ctx)
		logger.Error(err, "failed to stop workload on host", "hostID", workload.Status.HostID, "workloadID", workload.Status.WorkloadID)
		// don't return error, we want to remove the finalizer anyway
		// this might leave a dangling workload on the host, but there's not much we can do about it if the host is down
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=workloads,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=workloads/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=workloads/finalizers,verbs=update

func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	reconciler := condition.NewConditionedReconciler(
		r.Client,
		r.Scheme,
		&runtimev1alpha1.Workload{},
		workloadReconcileInterval)
	reconciler.SetFinalizer(workloadFinalizerName, r.finalize)

	reconciler.SetCondition(runtimev1alpha1.WorkloadConditionConfig, r.reconcileConfig)
	reconciler.SetCondition(runtimev1alpha1.WorkloadConditionHostSelection, r.reconcileHostSelection)
	reconciler.SetCondition(runtimev1alpha1.WorkloadConditionPlacement, r.reconcilePlacement)
	reconciler.SetCondition(runtimev1alpha1.WorkloadConditionSync, r.reconcileSync)
	reconciler.SetCondition(condition.TypeReady, r.reconcileReady)

	r.reconciler = reconciler

	err := mgr.GetFieldIndexer().IndexField(context.Background(), &runtimev1alpha1.Host{}, workloadSchedulableHostsIndex, func(rawObj client.Object) []string {
		if host, ok := rawObj.(*runtimev1alpha1.Host); ok {
			if host.Status.IsAvailable() {
				return []string{string(condition.ConditionTrue)}
			}
		}
		return []string{}
	})
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.Workload{}).
		Named("workload-Replica").
		Complete(r)
}
