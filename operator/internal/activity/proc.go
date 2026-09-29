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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcSampler reads the pod's processes out of /proc. It sees the pod's
// processes rather than the node's only because the pod sets
// shareProcessNamespace, which is the whole reason that field is in the pod
// template.
type ProcSampler struct {
	// procRoot is /proc in production, and a fixture directory in tests.
	procRoot string

	// read is os.ReadFile in production. It is a field so a test can make
	// /proc/<pid>/io unreadable, which is the condition that decides whether a
	// process is judged on IO at all.
	read func(string) ([]byte, error)
}

// NewProcSampler reads the real /proc.
func NewProcSampler() *ProcSampler {
	return &ProcSampler{procRoot: "/proc", read: os.ReadFile}
}

// Snapshot reads every process the sampler can see.
//
// A process that cannot be read is skipped rather than raised: processes exit
// while /proc is being walked, so a missing file is the normal case and not a
// failure of the sample.
func (s *ProcSampler) Snapshot() ([]ProcSample, error) {
	entries, err := os.ReadDir(s.procRoot)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.procRoot, err)
	}

	samples := make([]ProcSample, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !isPID(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.procRoot, entry.Name())

		stat, err := s.read(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		sample, err := parseStat(string(stat))
		if err != nil {
			continue
		}

		// /proc/<pid>/io is readable only by a process that shares this one's
		// identity. When it is not, the process is judged on CPU alone, which
		// is why this is recorded rather than reported.
		if io, err := s.read(filepath.Join(dir, "io")); err == nil {
			if rchar, wchar, ok := parseIO(string(io)); ok {
				sample.IO, sample.IOValid = rchar+wchar, true
			}
		}
		samples = append(samples, sample)
	}
	return samples, nil
}

// isPID reports whether a /proc entry names a process, as opposed to one of the
// kernel's own files.
func isPID(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseStat reads the counters out of one /proc/<pid>/stat line.
//
// The file is the process's name in parentheses followed by its fields, and the
// name is whatever the process called itself — so it may hold spaces and
// parentheses of its own. The fields are therefore found from the last ')'
// rather than by splitting the whole line, or a process such as tmux, whose
// name is "(tmux: server)", would shift every field after it.
func parseStat(line string) (ProcSample, error) {
	open := strings.IndexByte(line, '(')
	closed := strings.LastIndexByte(line, ')')
	if open < 0 || closed < open {
		return ProcSample{}, fmt.Errorf("stat line has no command field")
	}

	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil {
		return ProcSample{}, fmt.Errorf("stat line has no pid: %w", err)
	}

	comm := line[open+1 : closed]
	fields := strings.Fields(line[closed+1:])

	// fields[0] is field 3 of the stat line (the process state), so field n
	// sits at fields[n-3].
	const (
		ttyNr     = 7
		utime     = 14
		stime     = 15
		cutime    = 16
		cstime    = 17
		starttime = 22
	)
	if len(fields) < starttime-2 {
		return ProcSample{}, fmt.Errorf("stat line for %s has %d fields after the command, want %d", comm, len(fields), starttime-2)
	}

	sample := ProcSample{PID: pid, Comm: comm}
	if sample.TTYNr, err = parseIntField(fields, ttyNr); err != nil {
		return ProcSample{}, err
	}
	if sample.StartTime, err = parseUintField(fields, starttime); err != nil {
		return ProcSample{}, err
	}
	var ticks uint64
	for _, field := range []int{utime, stime, cutime, cstime} {
		value, err := parseUintField(fields, field)
		if err != nil {
			return ProcSample{}, err
		}
		ticks += value
	}
	sample.CPU = ticks
	return sample, nil
}

func parseUintField(fields []string, statField int) (uint64, error) {
	value, err := strconv.ParseUint(fields[statField-3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("stat field %d: %w", statField, err)
	}
	return value, nil
}

func parseIntField(fields []string, statField int) (int64, error) {
	value, err := strconv.ParseInt(fields[statField-3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("stat field %d: %w", statField, err)
	}
	return value, nil
}

// parseIO pulls the byte counters out of a /proc/<pid>/io file. Only rchar and
// wchar are wanted: they count bytes the process asked for, which is progress,
// whereas the syscall counters count requests and would call a poll loop busy.
func parseIO(text string) (rchar, wchar uint64, ok bool) {
	for line := range strings.SplitSeq(text, "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "rchar":
			rchar, ok = parsed, true
		case "wchar":
			wchar = parsed
		}
	}
	return rchar, wchar, ok
}
