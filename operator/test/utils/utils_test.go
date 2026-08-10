package utils

import "testing"

const expectedTestContext = "kind-cubestack-test-e2e"

func TestContextEquals(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected string
		want     bool
	}{
		{"trailing newline from kubectl", expectedTestContext + "\n", expectedTestContext, true},
		{"exact match", expectedTestContext, expectedTestContext, true},
		{"leading and trailing whitespace", "  " + expectedTestContext + "\t\n", expectedTestContext, true},
		{"different context", "kind-other-cluster", expectedTestContext, false},
		{"empty output", "", expectedTestContext, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContextEquals(tt.output, tt.expected); got != tt.want {
				t.Errorf("ContextEquals(%q, %q) = %v, want %v", tt.output, tt.expected, got, tt.want)
			}
		})
	}
}
