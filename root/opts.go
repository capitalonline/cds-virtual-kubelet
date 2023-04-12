package root

import (
	"encoding/json"
	"github.com/capitalonline/cds-virtual-kubelet/eci"
	"os"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
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
)

// Opts stores all the options for configuring the root virtual-kubelet command.
// It is used for setting flag values.
//
// You can set the default options by creating a new `Opts` struct and passing
// it into `SetDefaultOpts`
type Opts struct {
	// Path to the kubeconfig to use to connect to the Kubernetes API server.
	KubeConfigPath string
	// Namespace to watch for pods and other resources
	KubeNamespace string
	// Sets the port to listen for requests from the Kubernetes API server
	ListenPort int32

	// Node name to use when creating a node in Kubernetes
	NodeName string
	NodeId   string

	// Operating system to run pods for
	OperatingSystem string

	Provider string

	Taints []VKTaint
	//TaintKey    string
	//TaintEffect string
	// DisableTaint bool

	MetricsAddr string

	// Number of workers to use to handle pod notifications
	PodSyncWorkers       int
	InformerResyncPeriod time.Duration

	// Use node leases when supported by Kubernetes (instead of node status updates)
	EnableNodeLease bool

	TraceExporters  []string
	TraceSampleRate string
	TraceConfig     TracingExporterOptions

	// Startup Timeout is how long to wait for the kubelet to start
	StartupTimeout time.Duration

	CertPath string
	KeyPath  string

	Version string
}

type VKTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

// SetDefaultOpts sets default options for unset values on the passed in option struct.
// Fields tht are already set will not be modified.
func SetDefaultOpts(c *Opts) error {
	c.OperatingSystem = DefaultOperatingSystem
	c.Provider = ProviderName
	c.NodeName = eci.NodeName
	c.NodeId = eci.NodeId

	c.InformerResyncPeriod = DefaultInformerResyncPeriod

	c.TraceConfig.ServiceName = eci.NodeName
	c.MetricsAddr = DefaultMetricsAddr
	c.ListenPort = DefaultListenPort

	c.PodSyncWorkers = DefaultPodSyncWorkers
	i := os.Getenv("WORKERS")
	if i != "" {
		workers, _ := strconv.Atoi(i)
		if workers != 0 {
			c.PodSyncWorkers = workers
		}
	}

	c.KubeNamespace = DefaultKubeNamespace
	vkTaintStr := os.Getenv("TAINTS")
	if vkTaintStr == "" {
		c.Taints = []VKTaint{
			VKTaint{
				Key:    DefaultTaintKey,
				Value:  ProviderName,
				Effect: DefaultTaintEffect,
			},
		}
	} else {
		var l []VKTaint
		err := json.Unmarshal([]byte(vkTaintStr), &l)
		if err != nil {
			c.Taints = []VKTaint{
				VKTaint{
					Key:    DefaultTaintKey,
					Value:  ProviderName,
					Effect: DefaultTaintEffect,
				},
			}
		} else {
			l = append(l, VKTaint{
				Key:    DefaultTaintKey,
				Value:  ProviderName,
				Effect: DefaultTaintEffect,
			})
			c.Taints = l
		}
	}

	c.KubeConfigPath = getEnv("KUBECONFIG", "/home/cck/.kube/config")

	c.CertPath = getEnv("CERT_PATH", "/etc/kubernetes/pki/ca.crt")

	c.KeyPath = getEnv("KEY_PATH", "/etc/kubernetes/pki/ca.key")

	c.Version = "v1.0.0"
	return nil
}
