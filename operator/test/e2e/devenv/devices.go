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
// A cordoned node is left out. It goes on reporting its allocatable, but the
// scheduler will not place a pod on one, so counting it is the same mistake in a
// different shape: a cluster whose only device node is cordoned has no usable
// device whatever the arithmetic says.
func maxAllocatable(ctx context.Context, c client.Client) (map[corev1.ResourceName]int64, error) {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	out := make(map[corev1.ResourceName]int64, len(nodes.Items))
	for i := range nodes.Items {
		n := &nodes.Items[i]
		if n.Spec.Unschedulable {
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
