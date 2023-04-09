package root

import (
	"context"
	"strings"

	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/providers"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeFromProvider builds a kubernetes node object from a provider
// This is a temporary solution until node stuff actually split off from the provider interface itself.
func NodeFromProvider(ctx context.Context, name string, taints []v1.Taint, p providers.Provider, version string) *v1.Node {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"type":                   "virtual-kubelet",
				"kubernetes.io/role":     "agent",
				"beta.kubernetes.io/os":  strings.ToLower(p.OperatingSystem()),
				"kubernetes.io/hostname": name,
				"alpha.service-controller.kubernetes.io/exclude-balancer": "true",
			},
		},
		Spec: v1.NodeSpec{
			Taints: taints,
		},
		Status: v1.NodeStatus{
			NodeInfo: v1.NodeSystemInfo{
				OperatingSystem: p.OperatingSystem(),
				Architecture:    "amd64",
				KubeletVersion:  version,
			},
			Capacity:        p.Capacity(ctx),
			Allocatable:     p.Capacity(ctx),
			Conditions:      p.NodeConditions(ctx),
			Addresses:       p.NodeAddresses(ctx),
			DaemonEndpoints: *p.NodeDaemonEndpoints(ctx),
		},
	}
	return node
}

func getTaint(c Opts) ([]v1.Taint, error) {
	var taints []v1.Taint
	for _, v := range c.Taints {
		var effect corev1.TaintEffect
		switch v.Effect {
		case "NoSchedule":
			effect = corev1.TaintEffectNoSchedule
		case "NoExecute":
			effect = corev1.TaintEffectNoExecute
		case "PreferNoSchedule":
			effect = corev1.TaintEffectPreferNoSchedule
		default:
			return nil, errdefs.InvalidInputf("taint effect %q is not supported", v.Effect)
		}
		taints = append(taints, v1.Taint{
			Key:       v.Key,
			Value:     v.Value,
			Effect:    effect,
			TimeAdded: nil,
		})
	}
	return taints, nil
}
