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
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseStat", func() {
	// Fields after the name: state ppid pgrp session tty_nr tpgid flags minflt
	// cminflt majflt cmajflt utime stime cutime cstime priority nice
	// num_threads itrealvalue starttime. The ones this reads are tty_nr (7),
	// utime (14), stime (15), cutime (16), cstime (17) and starttime (22).
	const (
		statLine  = "1234 (tmux: server) S 1 1234 1234 34816 1234 4194304 100 0 0 0 10 20 30 40 20 0 1 0 998877"
		plainLine = "7 (python3) S 1 7 7 0 7 4194304 100 0 0 0 500 250 0 0 20 0 1 0 4242"
	)

	It("reads the counters from a name containing a space and a parenthesis", func() {
		// The kernel writes the process's own name, unquoted, so a name like
		// "(tmux: server)" shifts every field after it if the line is split
		// from the front. Parsing from the last ')' is what keeps this right.
		got, err := parseStat(statLine)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.PID).To(Equal(1234))
		Expect(got.Comm).To(Equal("tmux: server"))
		Expect(got.TTYNr).To(Equal(int64(34816)))
		Expect(got.StartTime).To(Equal(uint64(998877)))
		// utime+stime+cutime+cstime, the reaped children included.
		Expect(got.CPU).To(Equal(uint64(100)))
	})

	It("reads a process without a terminal", func() {
		got, err := parseStat(plainLine)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Comm).To(Equal("python3"))
		Expect(got.TTYNr).To(BeZero())
		Expect(got.CPU).To(Equal(uint64(750)))
		Expect(got.StartTime).To(Equal(uint64(4242)))
	})

	It("refuses a line it cannot read whole", func() {
		// A truncated line is a torn read of a file the kernel is rewriting. It
		// has to be refused rather than parsed into a process with no counters.
		_, err := parseStat("1234 (python3) S 1 1234")
		Expect(err).To(HaveOccurred())

		_, err = parseStat("not a stat line")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("parseIO", func() {
	It("reads the byte counters", func() {
		rchar, wchar, ok := parseIO("rchar: 1234\nwchar: 5678\nsyscr: 9\nsyscw: 10\n")
		Expect(ok).To(BeTrue())
		Expect(rchar).To(Equal(uint64(1234)))
		Expect(wchar).To(Equal(uint64(5678)))
	})

	It("reports nothing readable when the counters are absent", func() {
		_, _, ok := parseIO("syscr: 9\n")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("ProcSampler", func() {
	// A fixture /proc, so the walk, the skip-unreadable behaviour and the
	// io-is-optional path can all be exercised without a pod.
	var root string

	BeforeEach(func() {
		var err error
		root, err = os.MkdirTemp("", "proc")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(root)).To(Succeed()) })
	})

	write := func(pid, name, body string) {
		dir := filepath.Join(root, pid)
		Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)).To(Succeed())
	}

	// snapshot walks the fixture in place of the real /proc.
	snapshot := func() []ProcSample {
		samples, err := (&ProcSampler{procRoot: root, read: os.ReadFile}).Snapshot()
		Expect(err).NotTo(HaveOccurred())
		return samples
	}

	It("reads the processes and skips the kernel's own entries", func() {
		write("77", "stat", "77 (python3) S 1 77 77 0 77 1 100 0 0 0 500 250 0 0 20 0 1 0 4242")
		write("77", "io", "rchar: 1000\nwchar: 2000\n")
		write("cpuinfo", "stat", "not a process")

		samples := snapshot()
		Expect(samples).To(HaveLen(1))
		Expect(samples[0].PID).To(Equal(77))
		Expect(samples[0].IOValid).To(BeTrue())
		Expect(samples[0].IO).To(Equal(uint64(3000)))
	})

	It("judges a process without a readable io file on its counters anyway", func() {
		// /proc/<pid>/io is readable only by an identically-identified process,
		// so an unreadable one is ordinary. The process has to survive the
		// sample with IOValid false rather than drop out of it.
		write("77", "stat", "77 (python3) S 1 77 77 0 77 1 100 0 0 0 500 250 0 0 20 0 1 0 4242")

		samples := snapshot()
		Expect(samples).To(HaveLen(1))
		Expect(samples[0].IOValid).To(BeFalse())
		Expect(samples[0].CPU).To(Equal(uint64(750)))
	})

	It("skips a process that exits while it is being read", func() {
		write("77", "stat", "77 (python3) S 1 77 77 0 77 1 100 0 0 0 500 250 0 0 20 0 1 0 4242")
		write("88", "stat", "torn")
		write("99", "io", "rchar: 1\n")

		samples := snapshot()
		Expect(samples).To(HaveLen(1))
		Expect(samples[0].PID).To(Equal(77))
	})
})
