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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// watchJupyter is the set a jupyter environment would pass: the container's own
// port and the ssh one.
func watchJupyter() map[uint16]struct{} {
	return map[uint16]struct{}{8888: {}, 2222: {}}
}

var _ = Describe("parseProcNet", func() {
	const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

	It("reads an established connection and decodes both addresses", func() {
		// 22B8 is 8888 in plain hex; C001 is 49153. The address is the raw
		// bytes in host order, so 0100007F is 127.0.0.1 and not 1.0.0.127.
		table := header +
			"   1: 0100007F:22B8 0100007F:C001 01 00000000:00000000 00:00000000 00000000  1000        0 222 1 0000000000000000 100 0 0 10 0\n"

		conns := parseProcNet(table, watchJupyter())
		Expect(conns).To(HaveLen(1))
		Expect(conns[222].Local).To(Equal("127.0.0.1:8888"))
		Expect(conns[222].Remote).To(Equal("127.0.0.1:49153"))
		Expect(conns[222].State).To(Equal(stateEstablished))
	})

	It("ignores listening sockets and ports nobody asked about", func() {
		// A listening socket is there for as long as the server is, so counting
		// it would be counting the server's existence as the user's work.
		table := header +
			"   0: 00000000:22B8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0000000000000000 100 0 0 10 0\n" +
			"   2: 0100007F:1F90 0100007F:C002 01 00000000:00000000 00:00000000 00000000  1000        0 333 1 0000000000000000 100 0 0 10 0\n"

		Expect(parseProcNet(table, watchJupyter())).To(BeEmpty())
	})

	It("keeps a socket waiting to close, because the request it finished is work", func() {
		table := header +
			"   1: 0100007F:22B8 0100007F:C001 06 00000000:00000000 00:00000000 00000000  1000        0 444 1 0000000000000000 100 0 0 10 0\n"

		conns := parseProcNet(table, watchJupyter())
		Expect(conns).To(HaveLen(1))
		Expect(conns[444].State).To(Equal(stateTimeWait))
	})

	It("folds a v4-mapped v6 address onto its v4 form", func() {
		// A dual-stack server accepts on both families. Reading the same
		// connection as two sockets would make every step look like a change.
		table := header +
			"   1: 0000000000000000FFFF00000100007F:22B8 0000000000000000FFFF00000100007F:C001 01 00000000:00000000 00:00000000 00000000  1000        0 555 1 0000000000000000 100 0 0 10 0\n"

		conns := parseProcNet(table, watchJupyter())
		Expect(conns).To(HaveLen(1))
		Expect(conns[555].Local).To(Equal("127.0.0.1:8888"))
	})

	It("reads a real v6 address", func() {
		// 2001:db8::1. The kernel prints each 32-bit word of the address as a
		// host-order integer, so on a little-endian host every word is written
		// byte-reversed: 20010DB8 comes out as B80D0120, and the trailing 1
		// as 01000000. Written one word at a time so the reversal is visible.
		loopback := "B80D0120" + "00000000" + "00000000" + "01000000"
		table := header +
			"   1: " + loopback + ":22B8 00000000000000000000000000000000:0000 01 00000000:00000000 00:00000000 00000000  1000        0 666 1 0000000000000000 100 0 0 10 0\n"

		conns := parseProcNet(table, watchJupyter())
		Expect(conns).To(HaveLen(1))
		Expect(conns[666].Local).To(Equal("[2001:db8::1]:8888"))
	})

	It("skips a torn line rather than failing the read", func() {
		// The kernel rewrites this file as it is read, so a short line is
		// ordinary and must cost nothing but the line.
		table := header +
			"   1: 0100007F:22B8 0100007F:C001 01\n" +
			"   2: nonsense\n" +
			"   3: 0100007F:22B8 0100007F:C003 01 00000000:00000000 00:00000000 00000000  1000        0 777 1 0000000000000000 100 0 0 10 0\n"

		conns := parseProcNet(table, watchJupyter())
		Expect(conns).To(HaveLen(1))
		Expect(conns).To(HaveKey(uint64(777)))
	})
})
