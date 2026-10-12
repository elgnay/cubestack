//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/suanova/cubestack/api/v1alpha1"
	"github.com/suanova/cubestack/test/e2e/devenv"
)

// Family N: the GPU.
//
// Two things about a GPU environment are worth a cluster-level case, and only one
// of them can be asked anywhere. The first is that a spec naming a vendor's GPU
// gets one: scheduled onto a node that advertises the device, with the resource
// accounted to it, and visible to the tools and the framework the image is built
// around. That needs a node with the device, so those cases stand down where
// there is none.
//
// The second is what happens when the request cannot be met at all — the platform
// applies the spec as written, and the cluster says why it cannot serve it. That
// case needs no hardware, and it is here so that family N is never an empty run
// on a cluster without a GPU.

// gpuCount is what each positive case asks for.
//
// The CRD defaults `count` to 1, and every case below sets it explicitly rather
// than leaning on that default, so what is asserted is what the spec asked for
// and not what the API server filled in.
var gpuCount = int32(1)

// The extended resources the two vendors map to, named once so that the case
// which asks what a vendor's request renders to and the case which over-asks
// cannot come to disagree about it.
//
// Written out here rather than read from the controller's own table: this mapping
// is part of what the family checks, and a test that read the mapping out of the
// implementation would agree with it by construction.
const (
	nvidiaGPU = corev1.ResourceName("nvidia.com/gpu")
	metaxGPU  = corev1.ResourceName("metax-tech.com/gpu")
)

// gpuVendor is one vendor: what the cluster must advertise for the cases to run,
// which published image carries the vendor's own device tool, and the tool.
type gpuVendor struct {
	// device is the label value and the environment's name suffix.
	device string

	vendor aiv1alpha1.AcceleratorVendor

	// resource is the extended resource this vendor's request becomes.
	resource corev1.ResourceName

	image string

	// tool is the vendor's own device tool, run inside the environment.
	tool string
}

var gpuVendors = []gpuVendor{
	{
		device:   "nvidia",
		vendor:   aiv1alpha1.AcceleratorVendorNvidia,
		resource: nvidiaGPU,
		image:    "ssh-cuda-pytorch",
		tool:     "nvidia-smi -L",
	},
	{
		device:   "metax",
		vendor:   aiv1alpha1.AcceleratorVendorMetax,
		resource: metaxGPU,
		image:    "ssh-maca-pytorch",
		tool:     "mx-smi",
	},
}

// deviceCountCommand is the device count as the image's own torch sees it.
//
// `torch.cuda` rather than the vendor tool, because what this case is about is
// the framework a user's code runs against, not the driver's own view of itself.
// The MACA build is a port of the CUDA one, and whether that port mirrors
// `torch.cuda` is the part of this case a real MACA node has to confirm: the
// catalogue's own MACA stack check imports torch and prints `torch.__version__`
// and never touches a device API, so nothing in this repository settles it. If
// the port does not mirror it, this fails with the interpreter's own error and
// the finding is about the port rather than about the device.
const deviceCountCommand = `python3 -c 'import torch; print(torch.cuda.device_count())'`

// int32Ptr is a pointer to n, for the optional spec fields that are pointers.
func int32Ptr(n int64) *int32 {
	v := int32(n)
	return &v
}

func describeGPU() {
	Describe("GPU-dependent validation",
		Label(devenv.TierP1, devenv.LabelFamily("N")), Ordered,
		func() {
			// The probe is on the family rather than on each case because Ginkgo
			// runs an outer container's BeforeAll before the BeforeAll of every
			// case below it, which is what makes each gate closure read a map that
			// has been filled in rather than a nil one.
			BeforeAll(func(ctx SpecContext) {
				if conformance == nil {
					return
				}
				probeDevices(ctx)
			})

			for _, v := range gpuVendors {
				describeGPUVendor(v)
			}
			// Deliberately no device label: this case is about the resource's name
			// in the scheduler's own message, not about the resource being present.
			describeGPUShortfall()
		})
}

func describeGPUVendor(v gpuVendor) {
	Describe(v.device, Label(devenv.LabelDevice(v.device)), Ordered, func() {
		draftCase{
			Name:     "gpu-" + v.device,
			Image:    devenv.MustImage(v.image),
			Identity: devenv.NonRoot,
			Shape: func(d *aiv1alpha1.DevEnvironment) {
				d.Spec.Resources.GPU = &aiv1alpha1.GPUSpec{Vendor: v.vendor, Count: &gpuCount}
			},
			Available: func(context.Context) string {
				if deviceCapacity[v.resource] >= int64(gpuCount) {
					return ""
				}
				return fmt.Sprintf("no schedulable node advertises %s; this case needs %d",
					v.resource, gpuCount)
			},
			Cases: func(open func() *devenv.Environment) {
				It("N1 places it on a node with the device allocated to it",
					func(ctx SpecContext) {
						env := open()
						running(ctx, env)

						// The allocation is checked on the rendered pod because it
						// is what made the scheduler choose a node with a device:
						// without it the pod could be running on a node that has
						// none, and N2 would then fail for the wrong reason.
						pod, err := env.Pod(ctx)
						Expect(err).NotTo(HaveOccurred(),
							"reading the pod %s is running as", env.Name)
						assertPodAllocated(pod, v.resource, int64(gpuCount))
						Expect(pod.Spec.NodeName).NotTo(BeEmpty(),
							"the pod is placed on no node, so no node was asked for a device")
					})

				It("N2 runs the vendor's own device tool", func(ctx SpecContext) {
					env := open()

					// Exit 0 and something printed, rather than a parsed line,
					// because the two vendors mean different things by it and both
					// mean something: nvidia-smi is injected by the container
					// toolkit at start, so its running at all is the proof, while
					// mx-smi is baked into the image and so is proved by answering
					// rather than by existing. Parsing a device line would need a
					// sample of each tool's output, and the MACA one is not in this
					// repository to take.
					out := sshOutput(ctx, env, v.tool)
					Expect(strings.TrimSpace(out)).NotTo(BeEmpty(),
						"%s exited 0 and reported nothing, which is not a device being present", v.tool)
				})

				It("N3 lets the image's torch count the device", func(ctx SpecContext) {
					env := open()
					out := sshOutput(ctx, env, deviceCountCommand)
					Expect(strings.TrimSpace(out)).To(Equal(fmt.Sprintf("%d", gpuCount)),
						"the torch in %s counts a different number of devices from the spec", v.image)
				})
			},
		}.declare()
	})
}

// describeGPUShortfall is the case that runs on every cluster, GPU or not.
//
// It is the same shape as E9 — an environment the cluster cannot serve, reported
// as the cluster's problem rather than the spec's — and is here rather than in
// family E because of the one thing E9 does not check: the resource's own name.
// E9's subject is a claim about storage, so it asserts the scheduler's reason and
// that the message says what it could not find. The resource string a GPU request
// produces is family N's subject, and it is the string a user reads when their
// environment will not start.
func describeGPUShortfall() {
	draftCase{
		Name:     "gpu-shortfall",
		Image:    devenv.MustImage("ssh-cuda-pytorch"),
		Identity: devenv.NonRoot,
		Shape: func(d *aiv1alpha1.DevEnvironment) {
			// One more than the largest single node holds, which makes the request
			// unservable on any cluster rather than only on a small one. The
			// vendor is fixed rather than picked to suit the cluster: where no
			// node advertises it at all, the arithmetic asks for a single device
			// and the cluster refuses it for the same reason, so the case is never
			// vacuous — it is the one case in this family that always has
			// something to say.
			d.Spec.Resources.GPU = &aiv1alpha1.GPUSpec{
				Vendor: aiv1alpha1.AcceleratorVendorNvidia,
				Count:  int32Ptr(deviceCapacity[nvidiaGPU] + 1),
			}
		},
		Cases: func(open func() *devenv.Environment) {
			It("N4 reports an unservable request as the cluster's refusal, not the spec's",
				func(ctx SpecContext) {
					env := open()

					// The first reconcile records Accepted before the pod exists at
					// all, so this settles early and what the case then waits for is
					// the state that makes it mean something.
					c := acceptedSettled(ctx, env)
					Expect(c.Status).To(Equal(metav1.ConditionTrue),
						"a cluster that cannot serve the spec was reported as a spec the controller refused: %s",
						c.Message)
					Expect(c.Reason).To(Equal(devenv.ReasonAccepted),
						"the spec is applied as written; what cannot serve it is the cluster: %s", c.Message)

					// Where the refusal is reported instead. The pod has to exist
					// first, and a pod the scheduler keeps refusing never stops
					// existing, so this waits for the observation rather than for a
					// transition.
					var last error
					Eventually(func() error {
						last = env.Refresh(ctx)
						if last != nil {
							return last
						}
						pods, err := env.Pods(ctx)
						if err != nil {
							return err
						}
						if len(pods) == 0 {
							return fmt.Errorf("the pod has not been created yet")
						}
						return nil
					}).WithTimeout(5*time.Minute).WithPolling(5*time.Second).
						Should(Succeed(), "the environment's pod being created")

					Expect(env.Object().Status.Phase.Name).To(Equal(aiv1alpha1.PhasePending),
						"an environment waiting for a device it cannot get is waiting, not failed")

					pod, err := env.Pod(ctx)
					Expect(err).NotTo(HaveOccurred())
					cond := podCondition(pod, corev1.PodScheduled)
					Expect(cond).NotTo(BeNil(),
						"the pod records no PodScheduled condition, so nothing anywhere says why it is stuck")
					Expect(cond.Reason).To(Equal(corev1.PodReasonUnschedulable),
						"the scheduler's reason for refusing the pod: %s", cond.Message)
					Expect(cond.Message).To(ContainSubstring("Insufficient"),
						"the scheduler's message has to name what it could not find: %q", cond.Message)
					Expect(cond.Message).To(ContainSubstring(string(nvidiaGPU)),
						"the scheduler reports the resource it could not find, and that name is the contract: %q",
						cond.Message)
				})
		},
	}.declare()
}
