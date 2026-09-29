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

package activity

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
)

const (
	// AnnotationKey is the whole of the agent's output, and the whole of the
	// interface between it and the controller: a UTC RFC3339 timestamp at second
	// resolution, meaning "this environment was last seen active at this
	// instant". It is defined here rather than next to the controller that reads
	// it because it is the agent's contract, and because the agent's image must
	// not carry the controller's reconcilers to learn its own output.
	AnnotationKey = "ai.cubestack.io/last-activity"

	// SampleInterval is how often the environment's processes and sockets are
	// read. It is the resolution of the judgement: something that starts and
	// finishes between two samples leaves its CPU in its parent's reaped-child
	// counters, so it is not lost, but the moment it happened is.
	SampleInterval = 15 * time.Second

	// MinWriteInterval bounds how often the annotation is written. Sampling is
	// cheap and local; a write is a PATCH to the apiserver, and without a floor
	// a busy environment would produce one every fifteen seconds for as long as
	// it was busy.
	MinWriteInterval = 60 * time.Second

	// defaultWriteTimeout bounds one PATCH, so an apiserver that stops answering
	// cannot wedge the loop. A tick that is skipped is one sample, which is
	// nothing next to an annotation that is never written again.
	defaultWriteTimeout = 10 * time.Second
)

// Stepper is what the reporter needs from the detector: one sample, and what
// moved since the previous one.
type Stepper interface {
	Step() (Observation, error)
}

// AnnotationStore is the environment's last-activity annotation, read and
// written through the apiserver.
type AnnotationStore interface {
	// Current reports the annotation as it stands, or "" when it is absent.
	Current(ctx context.Context) (string, error)
	// Set writes the annotation.
	Set(ctx context.Context, value string) error
}

// Recorder turns the detector's judgement into the one thing that crosses the
// pod boundary: the ai.cubestack.io/last-activity annotation.
//
// Its central rule is that it never writes while the environment is idle. The
// annotation is a "last seen active" mark, not a heartbeat — an idle
// environment's annotation simply stops moving, and that staleness is the whole
// of the signal the controller reads. Writing the current time unconditionally
// would make it track the wall clock and no environment would ever time out.
type Recorder struct {
	detector Stepper
	store    AnnotationStore
	log      logr.Logger

	minWrite     time.Duration
	writeTimeout time.Duration

	// lastWritten is the annotation as it stands, seeded from the pod at
	// startup so a restarted agent does not rewrite a value that is already
	// there. lastWrite is when this process last wrote, which is what the floor
	// is measured against.
	lastWritten string
	lastWrite   time.Time
}

// NewRecorder builds a Recorder over a detector and the pod's annotation.
func NewRecorder(detector Stepper, store AnnotationStore, log logr.Logger) *Recorder {
	return &Recorder{
		detector:     detector,
		store:        store,
		log:          log,
		minWrite:     MinWriteInterval,
		writeTimeout: defaultWriteTimeout,
	}
}

// Run samples on every tick until ctx is cancelled or ticks is closed.
//
// It has no error to report and never panics out. The sidecar it runs in is not
// allowed to fail: the controller iterates every container status when it
// decides whether an environment has failed, so a sidecar in
// CrashLoopBackOff marks the whole environment Failed and stops the user's
// work. Every failure here is therefore logged and waited out, and the next
// tick tries again.
func (r *Recorder) Run(ctx context.Context, ticks <-chan time.Time) {
	if current, err := r.store.Current(ctx); err != nil {
		r.log.Error(err, "Could not read the current last-activity annotation; the next active sample will write one")
	} else {
		r.lastWritten = current
	}
	r.log.Info("Watching for activity",
		"sampleInterval", SampleInterval.String(),
		"minWriteInterval", r.minWrite.String(),
		"lastWritten", r.lastWritten)

	for {
		select {
		case <-ctx.Done():
			r.log.Info("Stopping", "lastWritten", r.lastWritten)
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			r.tick(ctx, now)
		}
	}
}

// tick takes one sample and writes the annotation if the environment looked
// active.
func (r *Recorder) tick(ctx context.Context, now time.Time) {
	defer func() {
		if rec := recover(); rec != nil {
			// A panic has to cost one sample and nothing more. Whatever the
			// samplers made of the pod, the alternative is a dead sidecar.
			r.log.Error(fmt.Errorf("%v", rec), "Recovered from a panic while sampling; this sample is discarded")
		}
	}()

	obs, err := r.detector.Step()
	if err != nil {
		r.log.Error(err, "Could not sample the environment")
		return
	}
	r.log.V(1).Info("Sampled the environment", "active", obs.Active, "reasons", obs.Reasons)
	if !obs.Active {
		return
	}
	if !r.mayWrite(now) {
		return
	}

	value := now.UTC().Format(time.RFC3339)
	writeCtx, cancel := context.WithTimeout(ctx, r.writeTimeout)
	defer cancel()
	if err := r.store.Set(writeCtx, value); err != nil {
		r.log.Error(err, "Could not record activity", "value", value)
		return
	}

	r.lastWritten, r.lastWrite = value, now
	r.log.V(1).Info("Recorded activity", "value", value, "reasons", obs.Reasons)
}

// mayWrite reports whether now is worth writing.
//
// The annotation only ever moves forward, so a time that is not after the value
// already there is not a write at all. That covers the identical value — which
// a floor measured in whole seconds would otherwise let through twice — and it
// covers a clock that has gone backwards, where a restarted agent reading a
// value a previous instance wrote ahead of it must not push the mark back to
// where its own clock thinks it is.
func (r *Recorder) mayWrite(now time.Time) bool {
	if previous, err := time.Parse(time.RFC3339, r.lastWritten); err == nil && !now.After(previous) {
		return false
	}
	// lastWrite is the zero time until this process writes, so the first active
	// sample always writes.
	return now.Sub(r.lastWrite) >= r.minWrite
}
