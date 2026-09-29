/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/suanova/cubestack/api/v1alpha1"
	"github.com/suanova/cubestack/internal/activity"
)

// stampActivity renders an instant the way the agent writes it into the pod
// annotation.
func stampActivity(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// activityPod fabricates the pod the idle judgement reads: the agent's mark as
// the pod annotation, plus the pod's own timestamps, which are what the baseline
// falls back to when there is no mark. An empty mark leaves the pod without the
// annotation entirely, which is what an environment nobody has worked in looks
// like.
func activityPod(mark string, startTime *metav1.Time, created time.Time) *corev1.Pod {
	var annotations map[string]string
	if mark != "" {
		annotations = map[string]string{activity.AnnotationKey: mark}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "de-idle-0", Namespace: testNamespace,
			Annotations: annotations, CreationTimestamp: metav1.NewTime(created),
		},
		Status: corev1.PodStatus{StartTime: startTime},
	}
}

// idleEnv builds the environment the specs below render or judge. It carries the
// timeout only when one is asked for, so an environment that never opted in is
// the default shape rather than a special case.
func idleEnv(running bool, timeout int32, marked bool) *aiv1alpha1.DevEnvironment {
	env := &aiv1alpha1.DevEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "de-idle", Namespace: testNamespace},
		Spec: aiv1alpha1.DevEnvironmentSpec{
			Type: aiv1alpha1.DevEnvironmentTypeVSCode, Image: testDevImage, Running: running,
		},
	}
	if timeout > 0 {
		env.Spec.Lifecycle = &aiv1alpha1.LifecycleSpec{IdleTimeout: timeout}
	}
	if marked {
		env.Annotations = map[string]string{autoStoppedAnnotationKey: autoStoppedValue}
	}
	return env
}

// The idle judgement is pure, and the clock is an argument, so every expectation
// below is a statement about arithmetic rather than about how long a spec took to
// run: no clock is injected and nothing is slept on.
var _ = Describe("decideIdle", func() {
	const timeout = time.Hour

	var (
		now  time.Time
		base time.Time
	)

	BeforeEach(func() {
		now = time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
		// Ninety seconds of a one-hour timeout have already gone by, which is the
		// ordinary case: an environment that is running and was active a moment
		// ago.
		base = now.Add(-90 * time.Second)
	})

	It("waits a whole check period while the deadline is further off than that", func() {
		Expect(decideIdle(now, true, timeout, base)).To(Equal(idleDecision{RequeueAfter: idleCheckPeriod}))
	})

	It("waits exactly until the deadline once it is nearer than a check period", func() {
		// 120s of timeout with 90s elapsed leaves 30s to wait. Requeuing on the
		// check period instead would stop the environment half a minute late.
		Expect(decideIdle(now, true, 2*time.Minute, base)).To(Equal(idleDecision{RequeueAfter: 30 * time.Second}))
		// A timeout shorter than the check period is the same arithmetic from the
		// other side, and the case where waiting a whole period would be worst.
		Expect(decideIdle(now, true, 100*time.Second, base)).To(Equal(idleDecision{RequeueAfter: 10 * time.Second}))
	})

	It("stops at the deadline rather than a check period after it", func() {
		Expect(decideIdle(now, true, 90*time.Second, base)).To(Equal(idleDecision{Stop: true}))
		Expect(decideIdle(now, true, time.Minute, base)).To(Equal(idleDecision{Stop: true}))
	})

	It("neither stops nor requeues an environment that is not running", func() {
		// A Pending, Failed or already-Stopped environment has nothing to stop,
		// and a marked one reports Stopped — so this is also what keeps a marked
		// environment from being judged a second time.
		Expect(decideIdle(now, false, timeout, base)).To(Equal(idleDecision{}))
	})

	It("does neither when the timeout is disabled", func() {
		Expect(decideIdle(now, true, 0, base)).To(Equal(idleDecision{}))
	})

	It("does neither when there is nothing to measure from", func() {
		// No pod at all: the pod watch, not a timer, is what will notice one.
		Expect(decideIdle(now, true, timeout, time.Time{})).To(Equal(idleDecision{}))
	})

	It("does not stop an environment whose clock reads ahead of the controller's", func() {
		Expect(decideIdle(now, true, timeout, now.Add(time.Hour))).To(Equal(idleDecision{RequeueAfter: idleCheckPeriod}))
	})
})

var _ = Describe("lastActivityAt", func() {
	var now time.Time

	BeforeEach(func() {
		now = time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	})

	It("reads the instant the agent wrote", func() {
		at := now.Add(-10 * time.Minute)
		got, ok := lastActivityAt(activityPod(stampActivity(at), nil, time.Time{}))
		Expect(ok).To(BeTrue())
		Expect(got).To(BeTemporally("==", at))
	})

	It("reports nothing to measure when the pod carries no mark", func() {
		_, ok := lastActivityAt(activityPod("", nil, time.Time{}))
		Expect(ok).To(BeFalse())
	})

	It("reports nothing when the mark does not parse", func() {
		// A mark that cannot be read must not stop anything: it falls back to the
		// pod's own timestamps rather than failing, the rule the agent holds
		// itself to as well.
		_, ok := lastActivityAt(activityPod("not-a-time", nil, time.Time{}))
		Expect(ok).To(BeFalse())
	})

	It("reports nothing when there is no pod", func() {
		_, ok := lastActivityAt(nil)
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("activityBaseline", func() {
	var now time.Time

	BeforeEach(func() {
		now = time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	})

	It("measures from the agent's mark when the pod carries one", func() {
		at := now.Add(-10 * time.Minute)
		// The pod started long before that, and the mark is the newer truth.
		start := metav1.NewTime(now.Add(-time.Hour))
		Expect(activityBaseline(activityPod(stampActivity(at), &start, start.Time))).To(BeTemporally("==", at))
	})

	It("falls back to when the container started", func() {
		start := metav1.NewTime(now.Add(-time.Hour))
		Expect(activityBaseline(activityPod("", &start, now))).To(BeTemporally("==", start.Time))
	})

	It("falls back to when the pod was created when no start is recorded", func() {
		// What a fabricated pod has: Status.StartTime is set by the kubelet, so a
		// pod that never reached one has only its creation to go on.
		created := now.Add(-time.Hour)
		Expect(activityBaseline(activityPod("", nil, created))).To(BeTemporally("==", created))
	})

	It("falls back to the start when the mark does not parse", func() {
		start := metav1.NewTime(now.Add(-time.Hour))
		Expect(activityBaseline(activityPod("not-a-time", &start, now))).To(BeTemporally("==", start.Time))
	})

	It("has nothing to measure from without a pod", func() {
		Expect(activityBaseline(nil)).To(BeZero())
	})
})

var _ = Describe("syncLastActivity", func() {
	var now time.Time

	BeforeEach(func() {
		now = time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	})

	It("records the agent's mark on status", func() {
		at := now.Add(-10 * time.Minute)
		status := &aiv1alpha1.DevEnvironmentStatus{}
		syncLastActivity(status, activityPod(stampActivity(at), nil, time.Time{}))
		Expect(status.LastActivityTime).NotTo(BeNil())
		Expect(status.LastActivityTime.Time).To(BeTemporally("==", at))
	})

	It("never reports the pod start as an activity time", func() {
		// The fallback says when the pod appeared, not when anyone worked, and a
		// field named lastActivityTime must not claim it.
		status := &aiv1alpha1.DevEnvironmentStatus{}
		start := metav1.NewTime(now.Add(-time.Hour))
		syncLastActivity(status, activityPod("", &start, start.Time))
		Expect(status.LastActivityTime).To(BeNil())
	})

	It("leaves the record alone when the pod has nothing to say", func() {
		// The pod is gone after a stop, so there is no annotation to read; what
		// was recorded is what explains why the environment stopped.
		recorded := metav1.NewTime(now.Add(-time.Hour))
		status := &aiv1alpha1.DevEnvironmentStatus{LastActivityTime: &recorded}
		syncLastActivity(status, nil)
		Expect(status.LastActivityTime.Time).To(BeTemporally("==", recorded.Time))
	})

	It("never moves the record backwards", func() {
		recorded := metav1.NewTime(now.Add(-time.Minute))
		status := &aiv1alpha1.DevEnvironmentStatus{LastActivityTime: &recorded}
		syncLastActivity(status, activityPod(stampActivity(now.Add(-time.Hour)), nil, time.Time{}))
		Expect(status.LastActivityTime.Time).To(BeTemporally("==", recorded.Time))
	})
})

var _ = Describe("the auto-stop mark", func() {
	It("is read from the annotation the platform sets", func() {
		Expect(autoStopped(idleEnv(true, 0, true))).To(BeTrue())
	})

	It("is absent from an environment that carries no annotations", func() {
		env := idleEnv(true, 0, false)
		Expect(env.Annotations).To(BeNil())
		Expect(autoStopped(env)).To(BeFalse())
	})

	It("is not the annotation being present, but the value it carries", func() {
		env := idleEnv(true, 0, false)
		env.Annotations = map[string]string{autoStoppedAnnotationKey: "false"}
		Expect(autoStopped(env)).To(BeFalse())
	})

	It("scales the environment to zero without the spec being touched", func() {
		env := idleEnv(true, 60, true)
		Expect(*(&DevEnvironmentReconciler{}).desiredStatefulSet(env).Spec.Replicas).To(Equal(int32(0)))
		Expect(env.Spec.Running).To(BeTrue())
	})

	It("is what stands between a running environment and zero replicas", func() {
		// The same environment without the mark, so the spec above cannot pass
		// because replicas are zero whatever the mark says.
		env := idleEnv(true, 60, false)
		Expect(*(&DevEnvironmentReconciler{}).desiredStatefulSet(env).Spec.Replicas).To(Equal(int32(1)))
	})

	It("is not needed to scale an environment the spec already stops", func() {
		env := idleEnv(false, 60, false)
		Expect(*(&DevEnvironmentReconciler{}).desiredStatefulSet(env).Spec.Replicas).To(Equal(int32(0)))
	})
})
