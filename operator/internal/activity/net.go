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
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Socket states in /proc/net/tcp. Only these two are watched. A listening
// socket is not activity — it is there for as long as the server is — and the
// transitional states are too brief to mean anything.
const (
	stateEstablished = "01"
	stateTimeWait    = "06"
)

// ConnSampler reads the pod's TCP sockets from /proc/net/tcp{,6}.
//
// It reads the kernel's own table rather than running ss, because the sidecar
// image carries no iproute2 and because a table can be parsed in a test without
// a network namespace to run in. The pod shares one network namespace between
// its containers, so what it reads is the environment's sockets.
type ConnSampler struct {
	root string // /proc in production
	// watch is the set of local ports worth reporting. It is given to the
	// sampler rather than hardcoded so the controller and the agent cannot
	// disagree about them — the design document says to watch SSH on 22, but 22
	// is the published Service port and the container binds 2222, so an agent
	// with its own list would watch a port nothing ever uses.
	watch map[uint16]struct{}

	read func(string) ([]byte, error)
}

// NewConnSampler reads the real /proc for connections on the given local ports.
func NewConnSampler(ports []int32) *ConnSampler {
	watch := make(map[uint16]struct{}, len(ports))
	for _, port := range ports {
		if port > 0 && port <= 65535 {
			watch[uint16(port)] = struct{}{}
		}
	}
	return &ConnSampler{root: "/proc", watch: watch, read: os.ReadFile}
}

// Snapshot reports the watched sockets, keyed by inode and folded so that one
// socket is one entry — a dual-stack server accepting on both families must not
// produce two identities for the same connection.
func (s *ConnSampler) Snapshot() (map[uint64]Conn, error) {
	sockets := make(map[uint64]Conn)
	var firstErr error
	for _, name := range []string{"tcp", "tcp6"} {
		path := filepath.Join(s.root, "net", name)
		text, err := s.read(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("reading %s: %w", path, err)
			}
			continue
		}
		maps.Copy(sockets, parseProcNet(string(text), s.watch))
	}
	if len(sockets) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return sockets, nil
}

// parseProcNet reads one /proc/net/tcp{,6} table.
//
// A line that cannot be read is skipped. The kernel is updating the file as it
// is read, so a torn or short line is expected, and a socket table this falls
// behind on is worth less than a sample that fails.
func parseProcNet(text string, watch map[uint16]struct{}) map[uint64]Conn {
	conns := make(map[uint64]Conn)
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		// sl local_address rem_address st tx_queue:rx_queue tr tm->when retrnsmt uid timeout inode
		if len(fields) < 10 || fields[0] == "sl" {
			continue
		}
		if fields[3] != stateEstablished && fields[3] != stateTimeWait {
			continue
		}

		local, localPort, ok := parseProcNetAddr(fields[1])
		if !ok {
			continue
		}
		if _, watched := watch[localPort]; !watched {
			continue
		}
		remote, remotePort, ok := parseProcNetAddr(fields[2])
		if !ok {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}

		conns[inode] = Conn{
			Local:  netip.AddrPortFrom(local, localPort).String(),
			Remote: netip.AddrPortFrom(remote, remotePort).String(),
			State:  fields[3],
		}
	}
	return conns
}

// parseProcNetAddr reads one "address:port" field. The port is plain hex; the
// address is the raw bytes in host order, so it is little-endian within each
// 32-bit word — which is why it is decoded rather than read as text.
func parseProcNetAddr(field string) (netip.Addr, uint16, bool) {
	host, portText, ok := strings.Cut(field, ":")
	if !ok {
		return netip.Addr{}, 0, false
	}
	port, err := strconv.ParseUint(portText, 16, 16)
	if err != nil {
		return netip.Addr{}, 0, false
	}

	var addr netip.Addr
	switch len(host) {
	case 8:
		addr, ok = decodeProcNetV4(host)
	case 32:
		addr, ok = decodeProcNetV6(host)
	default:
		return netip.Addr{}, 0, false
	}
	if !ok {
		return netip.Addr{}, 0, false
	}
	// A v6 table also carries v4 connections as v4-mapped addresses. Folding
	// them keeps one connection to a dual-stack port from reading as two.
	return addr.Unmap(), uint16(port), true
}

func decodeProcNetV4(host string) (netip.Addr, bool) {
	var b [4]byte
	for i := range 4 {
		value, err := strconv.ParseUint(host[i*2:i*2+2], 16, 8)
		if err != nil {
			return netip.Addr{}, false
		}
		b[3-i] = byte(value)
	}
	return netip.AddrFrom4(b), true
}

func decodeProcNetV6(host string) (netip.Addr, bool) {
	var b [16]byte
	for word := range 4 {
		for i := range 4 {
			at := word*8 + i*2
			value, err := strconv.ParseUint(host[at:at+2], 16, 8)
			if err != nil {
				return netip.Addr{}, false
			}
			b[word*4+(3-i)] = byte(value)
		}
	}
	return netip.AddrFrom16(b), true
}
