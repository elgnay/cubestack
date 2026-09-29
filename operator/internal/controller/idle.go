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
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/suanova/cubestack/api/v1alpha1"
	"github.com/suanova/cubestack/internal/activity"
)

const (
	// autoStoppedAnnotationKey is the mark the controller puts on the
	// DevEnvironment — not on the pod, which is the very thing the stop deletes —
	// when an idle environment is stopped out from under spec.running. It is the
	// whole of the state: the StatefulSet's replicas and the reported phase are
	// each derived from it, and nothing else records that the stop happened,
	// which is what keeps the platform from rewriting user intent in the spec
	// (design §4.5, D7/DEV-27).
	//
	// Clearing it is the other half of the contract and the console's job. With
	// spec.running deliberately left true, the operator cannot tell a start from
	// an environment that is still idle, so a start that does not clear the mark
	// changes nothing it can observe.
	autoStoppedAnnotationKey = "ai.cubestack.io/auto-stopped"
	autoStoppedValue         = "true"

	// idleCheckPeriod is the longest the controller waits between looks at an
	// idle environment's clock, and the only RequeueAfter in this controller: a
	// genuinely idle environment produces no events at all — that is what idle
	// means — so no watch can wake the controller to stop it. It is not a
	// substitute for the pod and DevEnvironment watches, which still carry
	// everything else; the requeue exists only while the environment is Running,
	// the timeout is on and the mark is clear.
	idleCheckPeriod = 60 * time.Second
)

// idleDecision is what one look at an environment's idle clock came to.
type idleDecision struct {
	// Stop is true when the environment has been idle past its timeout and should
	// be marked auto-stopped now.
	Stop bool
	// RequeueAfter is when to look again, or zero for no requeue.
	RequeueAfter time.Duration
}

// decideIdle answers the whole of the idle question: has this environment been
// idle past its timeout, and when is it worth looking again?
//
// It is pure — the caller passes now — so every part of the judgement, the
// min(remaining, idleCheckPeriod) requeue included, is testable with no clock
// and no cluster.
//
// running is the phase just derived from the observed pod, not spec.running: an
// environment that is Pending, Failed or already Stopped has nothing to stop,
// and marking one would report a stop on an environment that never ran. A marked
// environment reports Stopped, so the judgement is not taken twice.
//
// baseline is the zero time when there is nothing to measure from; a missing pod
// is the pod watch's business, not a timer's.
func decideIdle(now time.Time, running bool, timeout time.Duration, baseline time.Time) idleDecision {
	if !running || timeout <= 0 || baseline.IsZero() {
		return idleDecision{}
	}
	remaining := baseline.Add(timeout).Sub(now)
	if remaining <= 0 {
		return idleDecision{Stop: true}
	}
	return idleDecision{RequeueAfter: min(remaining, idleCheckPeriod)}
}

// lastActivityAt is the instant the agent last saw the environment active, read
// from the pod annotation it writes. ok is false when the pod carries no such
// annotation, or carries one that does not parse: a mark the agent never wrote
// is no mark, and an unreadable one must not stop anything. The parse is the
// same one the agent applies to its own last write, so the two ends of the
// contract accept the same strings.
func lastActivityAt(pod *corev1.Pod) (time.Time, bool) {
	if pod == nil {
		return time.Time{}, false
	}
	raw := pod.Annotations[activity.AnnotationKey]
	if raw == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// activityBaseline is the instant the idle clock is measured from: the agent's
// last-activity mark when the pod carries one, and the pod's own start when it
// does not.
//
// The fallback is what stops an environment that was created and abandoned. The
// agent writes only when it sees activity, so a pod nothing has ever happened in
// carries no annotation at all — and an environment whose only workload is a
// denylisted daemon, an ssh-only environment nobody logs into, would otherwise
// never be stopped at all.
//
// It cannot stop a live environment early: a pod's start is the newest instant
// it could possibly have been used, so the fallback is never older than the
// truth. Status.StartTime is when the container actually started; a pod with no
// recorded start — a fabricated one, in a test — has only its CreationTimestamp.
func activityBaseline(pod *corev1.Pod) time.Time {
	if pod == nil {
		return time.Time{}
	}
	if at, ok := lastActivityAt(pod); ok {
		return at
	}
	if pod.Status.StartTime != nil {
		return pod.Status.StartTime.Time
	}
	return pod.CreationTimestamp.Time
}

// idleTimeoutOf is the environment's idle timeout, or zero when it has none.
// Only an environment with the feature on can carry the mark — turning the
// timeout off clears it — but nothing that reports on a mark should depend on
// that being true to read the value.
func idleTimeoutOf(env *aiv1alpha1.DevEnvironment) time.Duration {
	if env.Spec.Lifecycle == nil {
		return 0
	}
	return time.Duration(env.Spec.Lifecycle.IdleTimeout) * time.Second
}

// autoStopped reports whether the environment carries the auto-stop mark.
func autoStopped(env *aiv1alpha1.DevEnvironment) bool {
	return env.Annotations[autoStoppedAnnotationKey] == autoStoppedValue
}

// markAutoStopped writes the mark.
//
// The write is a JSON merge patch naming the one annotation, aimed at a fresh
// object that is an address and nothing more — the shape internal/activity uses
// for the pod annotation, and for the same reason: a patch computed from the
// object this reconcile read would carry the informer's copy of every other
// annotation back to the apiserver and undo whatever the console wrote in the
// meantime.
//
// The mark is deliberately not written onto env. This pass reports what it
// observed, which is a running environment; the pass that reads the mark back is
// the one that reports the stop. Nothing in between should see a state the
// cluster has not reached.
func (r *DevEnvironmentReconciler) markAutoStopped(ctx context.Context, env *aiv1alpha1.DevEnvironment) error {
	body, err := json.Marshal(map[string]any{"metadata": map[string]any{
		"annotations": map[string]string{autoStoppedAnnotationKey: autoStoppedValue},
	}})
	if err != nil {
		return fmt.Errorf("building the auto-stop patch: %w", err)
	}
	obj := &aiv1alpha1.DevEnvironment{ObjectMeta: metav1.ObjectMeta{Namespace: env.Namespace, Name: env.Name}}
	if err := r.Patch(ctx, obj, client.RawPatch(types.MergePatchType, body)); err != nil {
		return fmt.Errorf("marking %s/%s auto-stopped: %w", env.Namespace, env.Name, err)
	}
	return nil
}

// clearAutoStopped removes the mark. A null value deletes the key in a merge
// patch, so the same single-annotation shape serves both directions. The
// in-memory copy is dropped only once the apiserver has accepted the write, so
// the rest of this pass reads the state the cluster is actually in.
func (r *DevEnvironmentReconciler) clearAutoStopped(ctx context.Context, env *aiv1alpha1.DevEnvironment) error {
	body, err := json.Marshal(map[string]any{"metadata": map[string]any{
		"annotations": map[string]any{autoStoppedAnnotationKey: nil},
	}})
	if err != nil {
		return fmt.Errorf("building the auto-stop patch: %w", err)
	}
	obj := &aiv1alpha1.DevEnvironment{ObjectMeta: metav1.ObjectMeta{Namespace: env.Namespace, Name: env.Name}}
	if err := r.Patch(ctx, obj, client.RawPatch(types.MergePatchType, body)); err != nil {
		return fmt.Errorf("clearing the auto-stop mark on %s/%s: %w", env.Namespace, env.Name, err)
	}
	delete(env.Annotations, autoStoppedAnnotationKey)
	return nil
}

// clearSupersededAutoStop drops the mark when it no longer describes the
// environment.
//
// An auto-stop mark is not a state the controller keeps once the user has spoken
// for themselves. An explicit stop supersedes it, and so does turning the idle
// timeout off: with the feature off nothing would ever act on the mark again, and
// spec.running left true would hold the environment at zero replicas and phase
// Stopped with no way back but a client clearing the annotation by hand.
//
// Both are cases where the mark is stale rather than wrong — the stop it records
// really happened — which is why clearing it says nothing about spec.running, the
// field the user owns.
func (r *DevEnvironmentReconciler) clearSupersededAutoStop(ctx context.Context, env *aiv1alpha1.DevEnvironment) error {
	if !autoStopped(env) || (env.Spec.Running && activityAgentEnabled(env)) {
		return nil
	}
	return r.clearAutoStopped(ctx, env)
}

// syncLastActivity carries the agent's mark into status, so what the idle
// timeout is measured against is visible wherever the environment's state is
// read.
//
// Only a real parsed annotation is ever written here. The pod-start fallback the
// judgement uses is a measurement of when the pod appeared, not of when anyone
// worked, and a field named lastActivityTime must not report it.
//
// The value only ever moves forward. The agent's own writer only moves it
// forward too, so a maximum is the same value in the ordinary case; it differs
// in the one case that matters, when the pod is gone and carries no annotation
// at all, and a plain assignment would discard the fact that explains why the
// environment stopped.
func syncLastActivity(status *aiv1alpha1.DevEnvironmentStatus, pod *corev1.Pod) {
	at, ok := lastActivityAt(pod)
	if !ok {
		return
	}
	if status.LastActivityTime != nil && !at.After(status.LastActivityTime.Time) {
		return
	}
	// Normalised to UTC so the round trip through the apiserver is stable:
	// status is written only when it differs, and a differing location would make
	// the same instant compare as a change on every reconcile.
	t := metav1.NewTime(at.UTC())
	status.LastActivityTime = &t
}

// reconcileIdleStop looks at the idle clock and, when the environment has been
// idle past its timeout, marks it auto-stopped. Marking is all it does: the
// replicas and the phase that follow from the mark are derived on the next pass
// by the same code that derives them for every other environment (see
// desiredStatefulSet and setPhaseAndReady).
func (r *DevEnvironmentReconciler) reconcileIdleStop(ctx context.Context, env *aiv1alpha1.DevEnvironment, status *aiv1alpha1.DevEnvironmentStatus, pod *corev1.Pod) (idleDecision, error) {
	syncLastActivity(status, pod)

	// A pod on its way out is not evidence that anyone is working. This is what
	// protects a start: the mark has just been cleared, the previous pod is still
	// visible carrying the annotation that made it look idle, and without this
	// the judgement would fire again and re-mark the environment the user has
	// just started.
	if pod == nil || pod.DeletionTimestamp != nil || autoStopped(env) {
		return idleDecision{}, nil
	}

	running := status.Phase != nil && status.Phase.Name == aiv1alpha1.PhaseRunning

	decision := decideIdle(time.Now(), running, idleTimeoutOf(env), activityBaseline(pod))
	if !decision.Stop {
		return decision, nil
	}
	return decision, r.markAutoStopped(ctx, env)
}
