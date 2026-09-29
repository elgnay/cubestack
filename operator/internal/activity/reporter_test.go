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
	"errors"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// base is an arbitrary instant. It is whole seconds because the annotation is
// written at second resolution, so nothing here has to round.
var base = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// at is base shifted by whole seconds, and the unit is in the signature because
// it has to be. As a time.Duration it read as nanoseconds: at(60) landed in the
// same second as at(0), where the mark is written at second resolution, so the
// assertions about the interval between writes held for the wrong reason.
func at(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }

// stamp is the annotation value written for an instant.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// fakeStore stands in for the pod. It records what was written, which is the
// only thing the reporter does that anyone outside can see.
type fakeStore struct {
	current    string
	currentErr error
	setErr     error
	writes     []string
}

func (f *fakeStore) Current(context.Context) (string, error) { return f.current, f.currentErr }

func (f *fakeStore) Set(_ context.Context, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.writes = append(f.writes, value)
	f.current = value
	return nil
}

// flakyStore refuses the first few writes, so a test can watch the loop carry on
// past a failure it cannot do anything about.
type flakyStore struct {
	*fakeStore
	failures int
	attempts int
}

func (f *flakyStore) Set(ctx context.Context, value string) error {
	f.attempts++
	if f.attempts <= f.failures {
		return errors.New("the server could not find the requested resource")
	}
	return f.fakeStore.Set(ctx, value)
}

// script is a detector whose answers are written down rather than sampled.
//
// Build it with newScript. panicAt is the call to explode on and has to default
// to "never", which the field's own zero value cannot express — call zero is a
// real call, and a script that panicked on it by accident would still look
// almost right, skipping one tick.
type script struct {
	observations []Observation
	err          error
	panicAt      int
	calls        int
}

func newScript(observations ...Observation) *script {
	return &script{observations: observations, panicAt: -1}
}

func (s *script) Step() (Observation, error) {
	call := s.calls
	s.calls++
	if call == s.panicAt {
		panic("the sampler exploded")
	}
	if s.err != nil {
		return Observation{}, s.err
	}
	if call >= len(s.observations) {
		return Observation{}, nil
	}
	return s.observations[call], nil
}

func busy() Observation { return Observation{Active: true} }
func quiet() Observation {
	return Observation{}
}

// tickTimes returns offsets from base, every sample interval, starting at zero.
func tickTimes(count int) []time.Time {
	times := make([]time.Time, 0, count)
	for i := range count {
		times = append(times, at(i*int(SampleInterval.Seconds())))
	}
	return times
}

// repeat is count copies of one judgement, so a script can be built from a
// description of what the user did rather than a list of ticks.
func repeat(obs Observation, count int) []Observation {
	list := make([]Observation, 0, count)
	for range count {
		list = append(list, obs)
	}
	return list
}

// run feeds every tick to the recorder and returns once it has drained them.
//
// The ticks are all buffered before the recorder starts and the channel is
// closed behind them, so the loop runs to completion on values alone. There is
// no sleep in this file, no wall clock, and no assertion that could depend on
// how long anything took.
func run(store AnnotationStore, detector Stepper, times ...time.Time) {
	runWith(nil, store, detector, times...)
}

func runWith(tune func(*Recorder), store AnnotationStore, detector Stepper, times ...time.Time) {
	ticks := make(chan time.Time, len(times))
	for _, t := range times {
		ticks <- t
	}
	close(ticks)

	recorder := NewRecorder(detector, store, logr.Discard())
	if tune != nil {
		tune(recorder)
	}
	recorder.Run(context.Background(), ticks)
}

var _ = Describe("Recorder", func() {
	It("records activity and then leaves the mark alone while the user is away", func() {
		// The rule the whole feature rests on. Twenty idle windows after one
		// active one produce nothing further: if the annotation moved on its
		// own, it would track the wall clock and no environment would ever
		// time out.
		store := &fakeStore{}
		observations := append([]Observation{busy()}, repeat(quiet(), 20)...)

		run(store, newScript(observations...), tickTimes(21)...)

		Expect(store.writes).To(Equal([]string{stamp(base)}))
	})

	It("leaves the mark standing for exactly as long as the environment was idle", func() {
		// This is the quantity the controller reads, and the reason the
		// assertion is on a value rather than on a clock: five minutes of an
		// untouched environment put the mark five minutes in the past.
		store := &fakeStore{}
		observations := append(repeat(busy(), 2), repeat(quiet(), 19)...)
		times := tickTimes(21)

		run(store, newScript(observations...), times...)

		Expect(store.writes).To(HaveLen(1))
		written, err := time.Parse(time.RFC3339, store.writes[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(times[len(times)-1].Sub(written)).To(Equal(300 * time.Second))
	})

	It("keeps a minute between writes however busy the environment is", func() {
		// A write is a PATCH to the apiserver. Sampling every fifteen seconds
		// must not become a write every fifteen seconds.
		store := &fakeStore{}
		times := tickTimes(9) // 0s through 120s

		run(store, newScript(repeat(busy(), 9)...), times...)

		Expect(store.writes).To(Equal([]string{stamp(at(0)), stamp(at(60)), stamp(at(120))}))
	})

	It("moves a stale mark forward but never a mark that is already ahead", func() {
		// The annotation only ever moves forward. An agent restarted onto a
		// mark older than the moment it is sampling has to advance it, or the
		// environment would look freshly used forever after any hiccup; but one
		// that finds the mark ahead of its own clock — a node whose clock
		// stepped back — must leave it alone rather than pull it back.
		behind := &fakeStore{current: stamp(at(-3600))}
		run(behind, newScript(busy()), at(0))
		Expect(behind.writes).To(Equal([]string{stamp(at(0))}))

		ahead := &fakeStore{current: stamp(at(3600))}
		run(ahead, newScript(busy()), at(0))
		Expect(ahead.writes).To(BeEmpty())
	})

	It("does not rewrite the mark a previous agent left behind", func() {
		// A restarted sidecar is not the user coming back to work. The value it
		// finds is the value it would write, so writing it again is one PATCH
		// that says nothing. It is not frozen, though: the next sample that has
		// something to say still moves the mark on.
		store := &fakeStore{current: stamp(base)}
		run(store, newScript(busy()), at(0))
		Expect(store.writes).To(BeEmpty())

		moved := &fakeStore{current: stamp(base)}
		run(moved, newScript(busy()), at(30))
		Expect(moved.writes).To(Equal([]string{stamp(at(30))}))
	})

	It("retries on the next sample when the apiserver refuses the write", func() {
		// Two failures and the environment is still running. The write is
		// retried fifteen seconds later rather than at the next minute, so a
		// blip costs one sample rather than a minute of the mark standing
		// still.
		store := &flakyStore{fakeStore: &fakeStore{}, failures: 2}

		run(store, newScript(repeat(busy(), 3)...), at(0), at(15), at(30))

		Expect(store.attempts).To(Equal(3))
		Expect(store.writes).To(Equal([]string{stamp(at(30))}))
	})

	It("carries on when the environment cannot be sampled at all", func() {
		// A pod whose processes could not be read is not an idle pod. Leaving
		// the mark where it is costs nothing; writing one would be inventing
		// activity, and stopping would be a failed sidecar.
		store := &fakeStore{}
		detector := newScript(busy(), busy())
		detector.err = errors.New("permission denied")

		run(store, detector, at(0), at(60))

		Expect(store.writes).To(BeEmpty())
	})

	It("survives a panic while sampling", func() {
		// The sidecar is not allowed to fail. A panic has to cost the sample it
		// happened in and nothing else, or a bug in a parser takes down the
		// environment rather than one reading of it.
		store := &fakeStore{}
		detector := newScript(busy(), busy())
		detector.panicAt = 0

		run(store, detector, at(0), at(60))

		Expect(detector.calls).To(Equal(2))
		Expect(store.writes).To(Equal([]string{stamp(at(60))}))
	})

	It("abandons a write that the apiserver never answers", func() {
		// An apiserver that accepts a connection and then says nothing must not
		// be able to wedge the loop: the sample after it still has to happen.
		store := &blockingStore{}
		detector := newScript(busy(), busy())

		runWith(func(r *Recorder) { r.writeTimeout = time.Millisecond }, store, detector, at(0), at(60))

		Expect(store.attempts).To(Equal(2))
		Expect(store.writes).To(BeEmpty())
	})
})

// blockingStore is an apiserver that never answers. Set returns when the
// caller's deadline expires, which is what makes the recorder's write timeout
// observable.
type blockingStore struct {
	attempts int
	writes   []string
}

func (b *blockingStore) Current(context.Context) (string, error) { return "", nil }

func (b *blockingStore) Set(ctx context.Context, value string) error {
	b.attempts++
	<-ctx.Done()
	return ctx.Err()
}
