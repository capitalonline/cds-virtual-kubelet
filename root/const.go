package root

import (
	corev1 "k8s.io/api/core/v1"
	"time"
)

// Defaults for root command options
const (
	ProviderName                = "cds-provider"
	DefaultOperatingSystem      = "Linux"
	DefaultInformerResyncPeriod = 1 * time.Minute
	DefaultMetricsAddr          = ":10255"
	DefaultListenPort           = 10250
	DefaultPodSyncWorkers       = 100
	DefaultKubeNamespace        = corev1.NamespaceAll

	DefaultTaintEffect = string(corev1.TaintEffectNoSchedule)
	DefaultTaintKey    = "virtual-kubelet.io/provider"

	DefaultKubeConfig = "/home/cck/.kube/config"
	DefaultCertPath   = "/etc/kubernetes/pki/ca.crt"
	DefaultPathPath   = "/etc/kubernetes/pki/ca.key"
)
