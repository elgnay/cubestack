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
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodAnnotationStore is the agent's view of the pod it runs in: one annotation,
// read and written through the apiserver.
type PodAnnotationStore struct {
	client client.Client
	pod    types.NamespacedName
}

// NewPodAnnotationStore reads and writes AnnotationKey on one pod.
func NewPodAnnotationStore(c client.Client, pod types.NamespacedName) *PodAnnotationStore {
	return &PodAnnotationStore{client: c, pod: pod}
}

// Current reports the annotation as it stands, or "" when the pod does not have
// it. A pod that has never been active has no annotation at all, which is not
// an error.
func (s *PodAnnotationStore) Current(ctx context.Context) (string, error) {
	var pod corev1.Pod
	if err := s.client.Get(ctx, s.pod, &pod); err != nil {
		return "", fmt.Errorf("reading pod %s: %w", s.pod, err)
	}
	return pod.Annotations[AnnotationKey], nil
}

// Set writes the annotation.
//
// The write is a JSON merge patch naming the one annotation this owns, rather
// than a read-modify-write of the whole object. A read would race every other
// writer on the pod — the controller sets annotations of its own, and a
// read-modify-write would put back whatever it read, undoing theirs — and it
// would need a verb this agent has no business holding. The patch needs only
// the annotation it names to be right.
func (s *PodAnnotationStore) Set(ctx context.Context, value string) error {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{AnnotationKey: value},
		},
	})
	if err != nil {
		return fmt.Errorf("building the annotation patch: %w", err)
	}

	// A merge patch carries only the metadata it changes, so the object sent is
	// an address and nothing more: the apiserver applies the body to the pod
	// with that name, and the empty spec is not part of a merge patch anyway.
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: s.pod.Namespace, Name: s.pod.Name}}
	if err := s.client.Patch(ctx, pod, client.RawPatch(types.MergePatchType, body)); err != nil {
		return fmt.Errorf("patching pod %s: %w", s.pod, err)
	}
	return nil
}
