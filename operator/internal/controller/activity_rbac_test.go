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

package controller

import (
	"os"
	"path/filepath"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	aiv1alpha1 "github.com/suanova/cubestack/api/v1alpha1"
)

// The manager may not grant what it does not itself hold: the API server
// refuses to create a Role whose rules exceed the creator's own permissions.
// The activity agent is the one place the controller delegates a verb it never
// uses — it grants get+patch on a single pod so the sidecar can record activity
// — so this test pins the delegation to the manager's own ClusterRole. When
// they drift, an environment with an idle timeout cannot be created at all: the
// Role is refused, and because that error returns before the status write, the
// object is left with no phase, no pod, and nothing to read (#258).
//
// It reads the generated ClusterRole rather than the kubebuilder marker, because
// the generated file is what reaches the cluster; `make manifests` keeps it in
// step with the marker, which is the change this test forces you to make.
func TestActivityAgentRoleIsGrantableByTheManager(t *testing.T) {
	env := &aiv1alpha1.DevEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: metav1.NamespaceDefault},
		Spec:       aiv1alpha1.DevEnvironmentSpec{Lifecycle: &aiv1alpha1.LifecycleSpec{IdleTimeout: 60}},
	}

	// A zero-value reconciler is enough: the rendered objects depend on the
	// environment, not on any manager configuration.
	var granted *rbacv1.Role
	for _, obj := range (&DevEnvironmentReconciler{}).desiredActivityAgentRBAC(env) {
		if role, ok := obj.(*rbacv1.Role); ok {
			granted = role
		}
	}
	if granted == nil {
		t.Fatal("desiredActivityAgentRBAC renders no Role, so this test would pass without checking anything")
	}

	manager := readManagerClusterRole(t)
	for _, rule := range granted.Rules {
		if !heldBy(manager, rule) {
			t.Errorf("the manager's ClusterRole does not cover %v on %v: the API server will refuse to create the activity agent's Role for every idle-enabled environment. Add the verb to the pods kubebuilder marker in devenvironment_controller.go, then run `make manifests` and `make helm-resources-update`.",
				rule.Verbs, rule.Resources)
		}
	}
}

// readManagerClusterRole loads the generated ClusterRole the manager runs as,
// from config/rbac — the source the chart's copy is regenerated from.
func readManagerClusterRole(t *testing.T) *rbacv1.ClusterRole {
	t.Helper()

	path := filepath.Join("..", "..", "config", "rbac", "role.yaml")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var role rbacv1.ClusterRole
	if err := yaml.NewYAMLOrJSONDecoder(f, 4096).Decode(&role); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if role.Kind != "ClusterRole" || len(role.Rules) == 0 {
		t.Fatalf("%s decoded to %s with %d rules; the generated file has moved or changed shape", path, role.Kind, len(role.Rules))
	}
	return &role
}

// heldBy reports whether the manager's ClusterRole may do everything a granted
// rule allows. A manager rule scoped by resourceNames cannot cover it: the
// granted names are the environment's pod names, which are not knowable here,
// so only an unscoped rule counts.
func heldBy(manager *rbacv1.ClusterRole, granted rbacv1.PolicyRule) bool {
	for _, rule := range manager.Rules {
		if len(rule.ResourceNames) > 0 {
			continue
		}
		if covers(rule.APIGroups, granted.APIGroups) &&
			covers(rule.Resources, granted.Resources) &&
			covers(rule.Verbs, granted.Verbs) {
			return true
		}
	}
	return false
}

// covers reports whether every wanted entry is present in held, reading "*" as
// covering everything.
func covers(held, wanted []string) bool {
	for _, w := range wanted {
		found := false
		for _, h := range held {
			if h == "*" || h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
