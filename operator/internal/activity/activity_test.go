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
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestActivity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Activity Suite")
}

// fakeConns and fakeProcs stand in for the pod: a Detector only ever reads the
// samplers, so one sample is a value and a sequence of samples is a script.
type fakeConns struct {
	sockets map[uint64]Conn
	err     error
}

func (f *fakeConns) Snapshot() (map[uint64]Conn, error) { return f.sockets, f.err }

type fakeProcs struct {
	procs []ProcSample
	err   error
}

func (f *fakeProcs) Snapshot() ([]ProcSample, error) { return f.procs, f.err }

// sample builds a process. cpu and io are absolute counter readings, which is
// what /proc holds; a test moves a process by raising them between steps.
func sample(pid int, comm string, start, cpu uint64, tty int64) ProcSample {
	return ProcSample{PID: pid, Comm: comm, StartTime: start, CPU: cpu, TTYNr: tty, IOValid: true}
}

var (
	// The agent's own pid, which must never count as activity however busy the
	// sampling makes it.
	selfPID = 42
	// A terminal: any non-zero tty_nr means the process is on one.
	onTTY = int64(34816)
	// A batch process: no controlling terminal.
	noTTY = int64(0)
)

// detector wires a Detector to a script whose samplers are replaced per step.
type detector struct {
	d     *Detector
	conns *fakeConns
	procs *fakeProcs
}

func newDetector(cfg Config) *detector {
	conns := &fakeConns{sockets: map[uint64]Conn{}}
	procs := &fakeProcs{}
	return &detector{d: NewDetector(cfg, conns, procs, selfPID), conns: conns, procs: procs}
}

// step runs one sample and returns whether it counted as activity.
func (x *detector) step(procs ...ProcSample) bool {
	x.procs.procs = procs
	return x.active()
}

func (x *detector) stepWith(conns map[uint64]Conn, procs ...ProcSample) bool {
	x.conns.sockets = conns
	x.procs.procs = procs
	return x.active()
}

func (x *detector) active() bool {
	obs, err := x.d.Step()
	Expect(err).NotTo(HaveOccurred())
	return obs.Active
}

var _ = Describe("Detector", func() {
	var cfg Config

	BeforeEach(func() {
		cfg = Config{CPUThreshold: 10, IOThreshold: 1024}
	})

	It("ignores everything on the first step", func() {
		// The first sample has no predecessor, so every process in it looks
		// newly started and every socket newly opened. Reporting that would
		// mean a restarted agent always looks like a user who just came back.
		x := newDetector(cfg)
		x.conns.sockets = map[uint64]Conn{1: {Local: "127.0.0.1:8888"}}
		Expect(x.step(sample(7, "bash", 100, 500, onTTY))).To(BeFalse())
	})

	It("does not count a socket that is merely still open", func() {
		// A JupyterLab or code-server tab holds its websocket open for as long
		// as the browser is open. If presence counted, an environment nobody
		// had touched for a week would never look idle.
		x := newDetector(cfg)
		open := map[uint64]Conn{1: {Local: "127.0.0.1:8888", Remote: "10.0.0.1:5000", State: stateEstablished}}
		idle := sample(7, "bash", 100, 500, onTTY)

		x.stepWith(open, idle) // seeding step
		for range 10 {
			Expect(x.stepWith(open, idle)).To(BeFalse())
		}
	})

	It("counts a socket appearing and a socket going away", func() {
		open := map[uint64]Conn{1: {Local: "127.0.0.1:2222", State: stateEstablished}}
		none := map[uint64]Conn{}
		idle := sample(7, "bash", 100, 500, onTTY)

		x := newDetector(cfg)
		x.stepWith(none, idle)
		Expect(x.stepWith(open, idle)).To(BeTrue())

		// A socket leaving is the request finishing, which is work too.
		Expect(x.stepWith(none, idle)).To(BeTrue())
	})

	It("counts any progress from a process on a terminal", func() {
		x := newDetector(cfg)
		x.step(sample(7, "bash", 100, 500, onTTY))
		Expect(x.step(sample(7, "bash", 100, 501, onTTY))).To(BeTrue())
	})

	It("counts a command that ran and exited from its parent's reaped-children time", func() {
		// A short command is gone by the next sample. Its CPU is only visible
		// in the shell's cutime/cstime, which sum() folds into CPU — without
		// them, a user working by typing commands would look idle.
		x := newDetector(cfg)
		x.step(sample(7, "bash", 100, 500, onTTY))
		Expect(x.step(sample(7, "bash", 100, 530, onTTY))).To(BeTrue())
	})

	It("requires a process without a terminal to clear the threshold", func() {
		x := newDetector(cfg)
		x.step(sample(7, "python3", 100, 500, noTTY))

		// Below the CPU threshold and moving no bytes: tooling that is merely
		// alive, not working.
		Expect(x.step(sample(7, "python3", 100, 505, noTTY))).To(BeFalse())
		Expect(x.step(sample(7, "python3", 100, 515, noTTY))).To(BeTrue())
	})

	It("counts a batch process that moves bytes without burning CPU", func() {
		// The dimension that catches a job writing results out while it waits.
		busy := sample(7, "python3", 100, 500, noTTY)
		busy.IO = 10_000
		more := busy
		more.IO = 20_000

		x := newDetector(cfg)
		x.step(busy)
		Expect(x.step(more)).To(BeTrue())
	})

	It("counts a new terminal process but not a new daemon", func() {
		x := newDetector(cfg)
		x.step()

		Expect(x.step(sample(7, "zsh", 100, 10, onTTY))).To(BeTrue())

		x = newDetector(cfg)
		x.step()
		Expect(x.step(sample(8, "containerd-shim", 100, 10, noTTY))).To(BeFalse())
	})

	It("never counts the agent's own process", func() {
		x := newDetector(cfg)
		x.step(sample(selfPID, "activity-agent", 100, 500, noTTY))
		Expect(x.step(sample(selfPID, "activity-agent", 100, 90_000, noTTY))).To(BeFalse())
	})

	It("never counts an ignored process whatever it does", func() {
		cfg.IgnoreComms = map[string]struct{}{"code-server": {}, "node": {}}
		x := newDetector(cfg)
		x.step(sample(7, "code-server", 100, 500, noTTY))
		Expect(x.step(sample(7, "code-server", 100, 90_000, noTTY))).To(BeFalse())
	})

	It("does not read a reused pid as the same process continuing", func() {
		// A recycled PID whose counters start low must not read as a process
		// that jumped backwards, and must not be compared against its
		// predecessor at all.
		x := newDetector(cfg)
		x.step(sample(7, "python3", 100, 90_000, noTTY))
		Expect(x.step(sample(7, "python3", 200, 5, noTTY))).To(BeFalse())
	})

	It("judges a process on CPU alone when its io cannot be read", func() {
		// /proc/<pid>/io is readable only by an identically-identified process.
		// When it is not, the CPU dimension has to carry the judgement.
		unreadable := sample(7, "python3", 100, 500, noTTY)
		unreadable.IOValid = false
		unreadable.IO = 0

		x := newDetector(cfg)
		x.step(unreadable)

		working := unreadable
		working.CPU = 515
		Expect(x.step(working)).To(BeTrue())

		// And the reverse: without an io reading there is nothing left to see
		// in a process that only moved bytes, so it must not be counted as
		// having done anything. The degradation has to be honest, not generous.
		x = newDetector(cfg)
		blind := sample(7, "python3", 100, 500, noTTY)
		blind.IOValid = false
		x.step(blind)
		stillBlind := blind
		stillBlind.IO = 90_000
		Expect(x.step(stillBlind)).To(BeFalse())
	})

	It("reports what moved even when it was not enough to count", func() {
		x := newDetector(cfg)
		x.step(sample(7, "python3", 100, 500, noTTY))
		x.procs.procs = []ProcSample{sample(7, "python3", 100, 505, noTTY)}

		obs, err := x.d.Step()
		Expect(err).NotTo(HaveOccurred())
		Expect(obs.Active).To(BeFalse())
		Expect(obs.Reasons).To(HaveLen(1))
		Expect(obs.Reasons[0]).To(ContainSubstring("python3"))
	})

	It("surfaces a sampler failure rather than reporting no activity", func() {
		// A pod whose processes could not be read is not an idle pod, and
		// saying so would stop someone's work on the strength of a read error.
		x := newDetector(cfg)
		x.procs.err = errors.New("permission denied")
		_, err := x.d.Step()
		Expect(err).To(MatchError(ContainSubstring("permission denied")))
	})
})
