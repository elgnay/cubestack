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

// Package activity decides whether a DevEnvironment is in use, from what the
// environment's own processes and sockets are doing.
//
// The judgement is made here rather than in the controller because the
// controller cannot see inside the pod: a sidecar sharing the pod's network and
// process namespaces can, and what it hands back is a single timestamp — the
// ai.cubestack.io/last-activity annotation. So this package is the whole of the
// judgement, and the annotation is the whole of its output.
//
// Activity is measured as progress, not as presence. A JupyterLab tab left open
// holds a websocket for as long as the browser is open, and a notebook server
// heartbeats by itself; if either counted, an environment nobody had touched
// for a week would never look idle, which is the opposite of what the timeout
// is for. What counts is a counter that moved: a process that burned CPU, or
// moved bytes, or a connection that came or went.
package activity

import (
	"fmt"
	"slices"
)

// Conn is one socket in the pod's network namespace.
type Conn struct {
	Local  string // "127.0.0.1:8888"
	Remote string
	State  string // the /proc/net/tcp state code: 01 established, 06 time_wait
}

// ConnectionSampler reports the pod's sockets. The map is keyed by socket
// inode, which is what identifies a socket across two samples: the inode
// survives, so the socket is the same one, and the addresses are only there to
// be read in a log.
type ConnectionSampler interface {
	Snapshot() (map[uint64]Conn, error)
}

// ProcSample is one process's counters at one instant.
type ProcSample struct {
	PID       int
	Comm      string
	StartTime uint64 // /proc/<pid>/stat field 22
	TTYNr     int64  // field 7; zero means the process has no controlling terminal

	// CPU is utime+stime+cutime+cstime in clock ticks. The reaped-children
	// counters are part of it because a command run from a shell has usually
	// exited by the next sample, and its CPU is then only visible in its
	// parent's cutime/cstime — without them, a user working by typing commands
	// into a terminal would look idle.
	CPU uint64

	// IO is rchar+wchar in bytes. Reading /proc/<pid>/io requires the reader to
	// share the process's identity, so IOValid is false when it could not be
	// read. That is an expected outcome rather than an error, and the process
	// degrades to being judged on CPU alone.
	IO      uint64
	IOValid bool
}

// ProcessSampler reports the pod's processes.
type ProcessSampler interface {
	Snapshot() ([]ProcSample, error)
}

// Config tunes the judgement. Thresholds exist because a pod is never truly
// doing nothing: servers poll, log and heartbeat on their own schedule, and
// something has to separate that from a user's work.
type Config struct {
	// CPUThreshold is the clock ticks a process without a controlling terminal
	// must burn within one sampling interval to count as activity.
	CPUThreshold uint64

	// IOThreshold is the bytes such a process must move within one sampling
	// interval to count. It is what catches a job that writes results while
	// using almost no CPU.
	IOThreshold uint64

	// IgnoreComms names processes that are never activity whatever they do.
	IgnoreComms map[string]struct{}

	// IgnorePIDs names processes that are never activity — the agent's own,
	// above all, so that reading /proc is not itself mistaken for the user
	// working.
	IgnorePIDs map[int]struct{}
}

// Detector compares consecutive samples of a pod. It holds the previous sample
// between calls, so one Detector belongs to one pod and is not safe for
// concurrent use.
type Detector struct {
	conns ConnectionSampler
	procs ProcessSampler
	cfg   Config

	prev     map[procKey]ProcSample
	prevConn map[uint64]Conn

	// seeded is false until a Step has completed. The first Step has no
	// predecessor, so everything in it would look newly started; skipping that
	// comparison is what keeps a restart of the agent from being reported as
	// the user springing back to life.
	seeded bool
}

// procKey identifies a process across samples. The start time is part of it
// because a PID alone does not: the kernel reuses PIDs, and a recycled one
// would otherwise read as the same process having jumped its counters.
type procKey struct {
	pid   int
	start uint64
}

// Observation is what one Step saw.
type Observation struct {
	// Active is true when something moved enough to count since the previous
	// Step.
	Active bool

	// Reasons names everything that moved, whether or not it was enough to
	// count, so the thresholds can be tuned against what environments really
	// run instead of against a guess. It is bounded, and is for the log only —
	// no decision is made from it.
	Reasons []string
}

// maxReasons bounds the per-step log line. A pod that suddenly runs a thousand
// short-lived processes should not produce a thousand of them.
const maxReasons = 8

// NewDetector builds a Detector over the given samplers, adding itself to the
// ignored PIDs so that the agent's own sampling — which is CPU it burns — is
// never mistaken for the user's.
func NewDetector(cfg Config, conns ConnectionSampler, procs ProcessSampler, self int) *Detector {
	ignored := make(map[int]struct{}, len(cfg.IgnorePIDs)+1)
	for pid := range cfg.IgnorePIDs {
		ignored[pid] = struct{}{}
	}
	ignored[self] = struct{}{}
	cfg.IgnorePIDs = ignored

	if cfg.IgnoreComms == nil {
		cfg.IgnoreComms = map[string]struct{}{}
	}
	return &Detector{conns: conns, procs: procs, cfg: cfg}
}

// Step samples the pod and reports what moved since the previous Step.
func (d *Detector) Step() (Observation, error) {
	var obs Observation

	conns, err := d.conns.Snapshot()
	if err != nil {
		return obs, fmt.Errorf("sampling connections: %w", err)
	}
	procs, err := d.procs.Snapshot()
	if err != nil {
		return obs, fmt.Errorf("sampling processes: %w", err)
	}

	active := false
	note := func(format string, args ...any) {
		if len(obs.Reasons) < maxReasons {
			obs.Reasons = append(obs.Reasons, fmt.Sprintf(format, args...))
		}
	}

	current := make(map[procKey]ProcSample, len(procs))
	for _, p := range procs {
		if _, skip := d.cfg.IgnorePIDs[p.PID]; skip {
			continue
		}
		if _, skip := d.cfg.IgnoreComms[p.Comm]; skip {
			continue
		}
		key := procKey{pid: p.PID, start: p.StartTime}
		current[key] = p

		if !d.seeded {
			continue
		}
		prev, seen := d.prev[key]
		if !seen {
			// Something appeared since the last sample. One with a terminal is
			// the user — a shell, an editor, a command they just started. One
			// without is a daemon the environment's own tooling forked, which
			// is not the user, however busy it turns out to be.
			if p.TTYNr != 0 {
				active = true
				note("started %s (pid %d, tty)", p.Comm, p.PID)
			}
			continue
		}

		cpu := counterDelta(p.CPU, prev.CPU)
		var io uint64
		if p.IOValid && prev.IOValid {
			io = counterDelta(p.IO, prev.IO)
		}
		if cpu == 0 && io == 0 {
			continue
		}
		note("%s (pid %d, tty %v) cpu+%d io+%d", p.Comm, p.PID, p.TTYNr != 0, cpu, io)

		if p.TTYNr != 0 {
			// A process with a terminal is the user's, so any progress at all
			// counts. Typing costs nothing, but running something costs at
			// least a tick, and that is the moment being looked for.
			if cpu > 0 || io > 0 {
				active = true
			}
			continue
		}
		// A process without a terminal has to clear a threshold, because
		// tooling that runs for as long as the pod does is never fully still.
		if cpu >= d.cfg.CPUThreshold || (p.IOValid && prev.IOValid && io >= d.cfg.IOThreshold) {
			active = true
		}
	}

	if d.seeded {
		for inode, c := range conns {
			if _, known := d.prevConn[inode]; !known {
				active = true
				note("opened %s -> %s", c.Local, c.Remote)
			}
		}
		for inode, c := range d.prevConn {
			if _, still := conns[inode]; !still {
				active = true
				note("closed %s -> %s", c.Local, c.Remote)
			}
		}
	}

	d.prev = current
	d.prevConn = conns
	d.seeded = true

	slices.Sort(obs.Reasons)
	obs.Active = active
	return obs, nil
}

// counterDelta is the movement between two readings of a kernel counter. The
// check for a decrease is defensive: the counters only rise, but a wrapped or
// unreadable one must not read as a jump big enough to look like activity.
func counterDelta(now, prev uint64) uint64 {
	if now < prev {
		return 0
	}
	return now - prev
}
