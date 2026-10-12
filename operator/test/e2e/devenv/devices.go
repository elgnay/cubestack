package devenv

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DeviceCapacity answers the one question a case about a device has to ask
// before it creates anything: does this cluster have the device.
//
// A case that needs a GPU or an RDMA fabric cannot run everywhere. The
// alternative to asking is to create an environment no node can host and leave
// it Pending in a cluster somebody else owns, so the question is asked first and
// a case that gets a no skips rather than fails: a device the cluster does not
// have is a property of the cluster, not a defect in the platform.
//
// Every resource a node advertises is reported, cpu and memory included, and a
// caller looks up the name it cares about. A name no node advertises is simply
// not in the map, so the lookup yields 0 either way — which is the answer to
// "absent" as much as to "all allocated". That is what lets one map serve both a
// case gated on a device being present and a case gated on it being missing.
func (s *Suite) DeviceCapacity(ctx context.Context) (map[corev1.ResourceName]int64, error) {
	s.capacityOnce.Do(func() {
		s.capacity, s.capacityErr = maxAllocatable(ctx, s.Client)
	})
	return s.capacity, s.capacityErr
}

// maxAllocatable is the query behind DeviceCapacity.
//
// It is the largest single node's allocatable, not the sum over all of them. Two
// nodes with one GPU each add up to two, but no single node can satisfy a
// request for two, so a sum would admit a case that then sits Pending — the
// outcome the gate exists to prevent.
//
// A node the environment's pod cannot be placed on is left out, which is a wider
// class than it first looks. A cordoned node goes on reporting its allocatable
// while the scheduler will refuse it, and so does a tainted one: the pod carries
// no tolerations, because nothing in the controller sets any, so a NoSchedule or
// NoExecute taint bars it just as surely. Counting either is the same mistake in
// a different shape — a cluster whose only device node is cordoned or tainted
// has no usable device whatever the arithmetic says.
//
// Both taints a node reports while it is not serving are NoExecute, so requiring
// the Ready condition as well would catch nothing this does not. Not checking it
// is deliberate rather than an omission, and the reason is worth stating: it
// could only ever exclude a node the rest of this function counted, so it would
// add a way for the gate to call a device absent without adding a way for it to
// notice one.
func maxAllocatable(ctx context.Context, c client.Client) (map[corev1.ResourceName]int64, error) {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	out := make(map[corev1.ResourceName]int64, len(nodes.Items))
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if n.Spec.Unschedulable || hasBlockingTaint(n.Spec.Taints) {
			continue
		}
		for name, q := range n.Status.Allocatable {
			if v := q.Value(); v > out[name] {
				out[name] = v
			}
		}
	}
	return out, nil
}

// hasBlockingTaint reports whether any of taints has an effect that keeps a pod
// off a node regardless of what it requests.
//
// PreferNoSchedule is not one of them and is deliberately not treated as one: it
// is a preference the scheduler weighs against the rest of the cluster, so
// calling it a bar would stand a case down on a cluster that could have run it.
func hasBlockingTaint(taints []corev1.Taint) bool {
	for _, t := range taints {
		if t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute {
			return true
		}
	}
	return false
}
