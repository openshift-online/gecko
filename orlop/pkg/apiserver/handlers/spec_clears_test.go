package handlers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

func TestPublicSpecClearsPreserveOtherFields(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	h := &ConvertingResourceHandler{privateScheme: scheme, gvk: corev1.SchemeGroupVersion.WithKind("Pod")}
	tests := []struct {
		name          string
		current       map[string]interface{}
		previous      map[string]interface{}
		clearAffinity bool
		removeMapKey  bool
	}{
		{
			name: "PUT explicit null", current: map[string]interface{}{"affinity": nil},
			clearAffinity: true,
		},
		{name: "PUT omission preserves existing fields", current: map[string]interface{}{}},
		{
			name: "PATCH removed object", current: map[string]interface{}{},
			previous: map[string]interface{}{"affinity": map[string]interface{}{}}, clearAffinity: true,
		},
		{
			name:    "PATCH removed map entry",
			current: map[string]interface{}{"nodeSelector": map[string]interface{}{"keep": "value"}},
			previous: map[string]interface{}{"nodeSelector": map[string]interface{}{
				"keep": "value", "remove": "value",
			}},
			removeMapKey: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The original contains fields unavailable in the public representation.
			// An integer beyond float64's exact range also must survive the round trip.
			const preciseInteger = int64(1<<53 + 1)
			original := &corev1.Pod{Spec: corev1.PodSpec{
				Affinity:              &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{}},
				NodeSelector:          map[string]string{"keep": "value", "remove": "value", "private": "hidden"},
				ActiveDeadlineSeconds: ptr.To(preciseInteger),
			}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
			result, err := h.applyPublicSpecClears(original, map[string]interface{}{"spec": tt.current}, map[string]interface{}{"spec": tt.previous})
			if err != nil {
				t.Fatal(err)
			}
			updated := result.(*corev1.Pod)
			if (updated.Spec.Affinity == nil) != tt.clearAffinity {
				t.Fatal("unexpected affinity removal")
			}
			_, hasRemovedKey := updated.Spec.NodeSelector["remove"]
			if hasRemovedKey == tt.removeMapKey {
				t.Fatal("unexpected map entry removal")
			}
			if updated.Spec.NodeSelector["private"] != "hidden" || updated.Status.Phase != corev1.PodRunning || *updated.Spec.ActiveDeadlineSeconds != preciseInteger {
				t.Fatal("clearing a public field changed private data or status")
			}
			if original.Spec.Affinity == nil || original.Spec.NodeSelector["remove"] != "value" {
				t.Fatal("clearing fields mutated the input object")
			}
		})
	}
}
