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

package main

import "testing"

func TestValidateRDMAResource(t *testing.T) {
	cases := []struct {
		value string
		ok    bool
	}{
		{"rdma/ib_shared_devices", true},
		{"rdma/roce_shared_devices", true},
		{"example.com/rdma", true},
		{"", false},
		{"rdma/", false},
		{"/hca", false},
		{"rdma/hca/extra", false},
		{"rdma/hca shared", false},
		{"cpu", false},
		{"memory", false},
		{"nvidia.com/gpu", false},
		{"metax-tech.com/gpu", false},
		{"requests.rdma/hca", false},
	}
	for _, c := range cases {
		err := validateRDMAResource("rdma-ib-resource", c.value)
		if c.ok && err != nil {
			t.Errorf("validateRDMAResource(%q) = %v, want nil", c.value, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validateRDMAResource(%q) = nil, want an error", c.value)
		}
	}
}
