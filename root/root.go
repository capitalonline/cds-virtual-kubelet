package root

import (
	"context"
	"github.com/capitalonline/cds-virtual-kubelet/eci"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/manager"
	"github.com/virtual-kubelet/virtual-kubelet/node"
	ps "github.com/virtual-kubelet/virtual-kubelet/providers"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/kubernetes/typed/coordination/v1beta1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"os"
	"path"
	"time"
)

// NewCommand creates a new top-level command.
// This command is used to start the virtual-kubelet daemon
func NewCommand(ctx context.Context, name string, c Opts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Short: name + " provides a virtual kubelet interface for your kubernetes cluster.",
		Long: name + ` implements the Kubelet interface with a pluggable
backend implementation allowing users to create kubernetes nodes without running the kubelet.
This allows users to schedule kubernetes workloads on nodes that aren't running Kubernetes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return RunRootCommand(ctx, c)
		},
	}

	installFlags(cmd.Flags(), &c)
	return cmd
}

func RunRootCommand(ctx context.Context, c Opts) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if ok := ps.ValidOperatingSystems[c.OperatingSystem]; !ok {
		return errdefs.InvalidInputf("operating system %q is not supported", c.OperatingSystem)
	}
	if c.PodSyncWorkers == 0 {
		return errdefs.InvalidInput("pod sync workers must be greater than 0")
	}
	var taints []corev1.Taint

	var err error
	taints, err = getTaint(c)
	if err != nil {
		return err
	}

	k8sClient, err := newClient(c.KubeConfigPath)
	if err != nil {
		return err
	}

	podInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(
		k8sClient,
		c.InformerResyncPeriod,
		kubeinformers.WithNamespace(c.KubeNamespace),
		kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", c.NodeName).String()
		}))
	podInformer := podInformerFactory.Core().V1().Pods()

	scmInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(k8sClient, c.InformerResyncPeriod)

	secretInformer := scmInformerFactory.Core().V1().Secrets()
	configMapInformer := scmInformerFactory.Core().V1().ConfigMaps()
	serviceInformer := scmInformerFactory.Core().V1().Services()

	go podInformerFactory.Start(ctx.Done())
	go scmInformerFactory.Start(ctx.Done())

	rm, err := manager.NewResourceManager(
		podInformer.Lister(),
		secretInformer.Lister(),
		configMapInformer.Lister(),
		serviceInformer.Lister(),
	)
	if err != nil {
		return errors.Wrap(err, "could not create resource manager")
	}

	apiConfig, err := getAPIConfig(c)
	if err != nil {
		return err
	}

	if err := setupTracing(ctx, c); err != nil {
		return err
	}

	eciProvider, err := eci.NewECIProvider(
		rm,
		c.NodeName,
		c.OperatingSystem,
		os.Getenv("POD_IP"),
		c.ListenPort,
	)
	if err != nil {
		return err
	}

	ctx = log.WithLogger(ctx, log.G(ctx).WithFields(log.Fields{
		"provider":         c.Provider,
		"operatingSystem":  c.OperatingSystem,
		"node":             c.NodeName,
		"watchedNamespace": c.KubeNamespace,
	}))

	var leaseClient v1beta1.LeaseInterface
	if c.EnableNodeLease {
		leaseClient = k8sClient.CoordinationV1beta1().Leases(corev1.NamespaceNodeLease)
	}

	pNode := NodeFromProvider(ctx, c.NodeName, taints, eciProvider, c.Version)
	nodeRunner, err := node.NewNodeController(
		node.NaiveNodeProvider{},
		pNode,
		k8sClient.CoreV1().Nodes(),
		node.WithNodeEnableLeaseV1Beta1(leaseClient, nil),
		node.WithNodeStatusUpdateErrorHandler(func(ctx context.Context, err error) error {
			if !k8serrors.IsNotFound(err) {
				return err
			}

			log.G(ctx).Debug("node not found")
			newNode := pNode.DeepCopy()
			newNode.ResourceVersion = ""
			_, err = k8sClient.CoreV1().Nodes().Create(newNode)
			if err != nil {
				return err
			}
			log.G(ctx).Debug("created new node")
			return nil
		}),
	)
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	eb := record.NewBroadcaster()
	eb.StartLogging(log.G(ctx).Infof)
	eb.StartRecordingToSink(&corev1client.EventSinkImpl{Interface: k8sClient.CoreV1().Events(c.KubeNamespace)})

	pc, err := node.NewPodController(node.PodControllerConfig{
		PodClient:       k8sClient.CoreV1(),
		PodInformer:     podInformer,
		EventRecorder:   eb.NewRecorder(scheme.Scheme, corev1.EventSource{Component: path.Join(pNode.Name, "pod-controller")}),
		Provider:        eciProvider,
		SecretLister:    secretInformer.Lister(),
		ConfigMapLister: configMapInformer.Lister(),
		ServiceLister:   serviceInformer.Lister(),
	})
	if err != nil {
		return errors.Wrap(err, "error setting up pod controller")
	}

	cancelHTTP, err := setupHTTPServer(ctx, eciProvider, apiConfig)
	if err != nil {
		return err
	}
	defer cancelHTTP()

	go func() {
		//  PodController 长时间执行runWorker，不断读取工作队列里面的数据
		if err := pc.Run(ctx, c.PodSyncWorkers); err != nil && errors.Cause(err) != context.Canceled {
			log.G(ctx).Fatal(err)
		}
	}()

	if c.StartupTimeout > 0 {
		err = waitFor(ctx, c.StartupTimeout, pc.Ready())
		if err != nil {
			return err
		}
	}

	go func() {
		// NodeController 创建vNode
		if err := nodeRunner.Run(ctx); err != nil {
			log.G(ctx).Fatal(err)
		}
	}()

	log.G(ctx).Info("Initialized")

	<-ctx.Done()
	return nil
}

func waitFor(ctx context.Context, time time.Duration, ready <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, time)
	defer cancel()

	log.G(ctx).Info("Waiting for pod controller / VK to be ready")

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "Error while starting up VK")
	}
}

func newClient(configPath string) (*kubernetes.Clientset, error) {
	var config *rest.Config

	// Check if the kubeConfig file exists.
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		// Get the kubeconfig from the filepath.
		config, err = clientcmd.BuildConfigFromFlags("", configPath)
		if err != nil {
			return nil, errors.Wrap(err, "error building client config")
		}
	} else {
		// Set to in-cluster config.
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, errors.Wrap(err, "error building in cluster config")
		}
	}

	if masterURI := os.Getenv("MASTER_URI"); masterURI != "" {
		config.Host = masterURI
	}

	return kubernetes.NewForConfig(config)
}
