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

	// autoStoppedAtAnnotationKey is the companion record of *when* the mark was
	// written, and it is deliberately a second key rather than the mark's value.
	// The mark is the client's to clear, so anything stored in it is gone at
	// exactly the moment it is needed — the pass that finds the mark cleared is
	// the pass that has to know when the stop it describes happened. This key is
	// the controller's own and no client writes or clears it.
	//
	// What it is for: a pod the platform has already stopped does not disappear
	// the instant the mark is written. It is still there for as long as the
	// scale-down takes, and while it is, it still carries the stale activity mark
	// that made it look idle in the first place. Clearing the mark is a start, and
	// a start must not be judged idle against the pod of the session before it
	// (::lastAutoStopAt).
	autoStoppedAtAnnotationKey = "ai.cubestack.io/auto-stopped-at"

	// autoStopStampFormat is the layout that key is written and read with, named
	// once so the two ends cannot drift apart — which they had: the writer used
	// time.RFC3339, which drops fractional seconds, so a stop at 12:00:00.999 was
	// recorded as 12:00:00 and the floor granted the environment up to a second
	// less than the timeout it asked for. The asymmetry is silent and the error is
	// always in the same direction, shortening a timeout, which is why a wrong
	// answer here looks like a working one.
	//
	// RFC3339Nano parses a value with or without a fraction, so the marks the
	// earlier build wrote are still read, and it renders a whole second without a
	// spurious suffix, so the ordinary case is unchanged.
	autoStopStampFormat = time.RFC3339Nano

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

// idleBaseline is the instant the idle clock is measured from: the pod's own
// activity, floored at the last stop this controller made.
//
// The floor is the second of the two guards that protect a start. A pod does not
// vanish when the mark is written — the scale-down lands a moment later, and
// until it does the pod this reconcile observes is the old one: running, ready,
// and still carrying the activity mark from the session that has just ended.
// Clearing the mark is a start, and a start must not be judged against the pod
// it replaced, so the clock measures from the later of the two
// (::lastAutoStopAt).
//
// It is a floor on the clock rather than a veto on the judgement: the
// environment is not kept awake, it is given the timeout over again from the
// stop — so a pod that really does survive its own stop is still stopped,
// one timeout later, rather than never.
func idleBaseline(env *aiv1alpha1.DevEnvironment, pod *corev1.Pod) time.Time {
	baseline := activityBaseline(pod)
	if stoppedAt := lastAutoStopAt(env); stoppedAt.After(baseline) {
		baseline = stoppedAt
	}
	return baseline
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

// lastAutoStopAt is the instant the controller last stopped this environment
// itself, or the zero time when it never has.
//
// It is a floor for the idle clock, and it exists because clearing the mark is
// not only a change of state, it is also the moment the environment's session
// restarts. The pod from before that instant is still visible for as long as the
// scale-down takes to land — and it is still carrying the activity mark that
// made it look idle, so judging it would re-mark the environment the user has
// just started. Measuring from the later of the pod's activity and this instant
// says the only thing that is true of both cases: everything the environment did
// before it was stopped belongs to the session that is over.
//
// An unreadable value is no value, for the same reason an unreadable pod mark is
// no mark: a record the controller cannot read must not be allowed to hold an
// environment awake, and must not be allowed to stop one either.
func lastAutoStopAt(env *aiv1alpha1.DevEnvironment) time.Time {
	raw := env.Annotations[autoStoppedAtAnnotationKey]
	if raw == "" {
		return time.Time{}
	}
	at, err := time.Parse(autoStopStampFormat, raw)
	if err != nil {
		return time.Time{}
	}
	return at
}

// markAutoStopped writes the mark, and with it the instant it was written.
//
// The write is a JSON merge patch naming the two annotations, aimed at a fresh
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
		"annotations": map[string]string{
			autoStoppedAnnotationKey:   autoStoppedValue,
			autoStoppedAtAnnotationKey: time.Now().UTC().Format(autoStopStampFormat),
		},
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

	// A pod on its way out is not evidence that anyone is working. This is the
	// first of the two guards that protect a start: the mark has just been
	// cleared, the previous pod is still visible carrying the annotation that made
	// it look idle, and without this the judgement would fire again and re-mark
	// the environment the user has just started.
	//
	// It covers the pod that is already terminating. The second guard covers the
	// one that is not yet — see the baseline below.
	if pod == nil || pod.DeletionTimestamp != nil || autoStopped(env) {
		return idleDecision{}, nil
	}

	running := status.Phase != nil && status.Phase.Name == aiv1alpha1.PhaseRunning

	decision := decideIdle(time.Now(), running, idleTimeoutOf(env), idleBaseline(env, pod))
	if !decision.Stop {
		return decision, nil
	}
	return decision, r.markAutoStopped(ctx, env)
}
