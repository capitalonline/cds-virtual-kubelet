package eci

import (
	"context"
	"fmt"
	"github.com/capitalonline/cds-virtual-kubelet/cdsapi"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/manager"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"io"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"strings"
	"sync"
)

// ECIProvider implements the virtual-kubelet provider interface and communicates with Alibaba Cloud's ECI APIs.
type ECIProvider struct {
	sync.RWMutex
	resourceManager    *manager.ResourceManager
	nodeName           string
	operatingSystem    string
	cpu                string
	memory             string
	maxPods            string
	internalIP         string
	daemonEndpointPort int32
}

// AuthConfig is the secret returned from an ImageRegistryCredential
type AuthConfig struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	Auth          string `json:"auth,omitempty"`
	Email         string `json:"email,omitempty"`
	ServerAddress string `json:"serveraddress,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
	RegistryToken string `json:"registrytoken,omitempty"`
}

// NewECIProvider creates a new ECIProvider.
func NewECIProvider(rm *manager.ResourceManager, nodeName, operatingSystem string, internalIP string, daemonEndpointPort int32) (*ECIProvider, error) {
	var p ECIProvider
	var err error

	p.resourceManager = rm

	p.cpu = "500000000"
	p.memory = "400Ti"
	p.maxPods = MaxPods

	p.operatingSystem = operatingSystem
	p.nodeName = nodeName
	p.internalIP = internalIP
	p.daemonEndpointPort = daemonEndpointPort

	return &p, err
}

// CreatePod accepts a Pod definition and creates an ECI deployment
func (p *ECIProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	if pod != nil && pod.OwnerReferences != nil && len(pod.OwnerReferences) != 0 && pod.OwnerReferences[0].Kind == "DaemonSet" {
		return fmt.Errorf("%s DaemonSet unsupported", pod.Name)
	}
	if pod.Status.Reason == "ProviderFailed" {
		return fmt.Errorf("%s", pod.Status.Message)
	}

	var (
		ownerMap = make(map[string]string)
		// simContainers []map[string]string
	)
	if pod != nil && pod.OwnerReferences != nil && len(pod.OwnerReferences) != 0 {
		ownerMap["kind"] = pod.OwnerReferences[0].Kind
		ownerMap["name"] = pod.OwnerReferences[0].Name
	}
	request := CreateContainerGroup{}
	request.RestartPolicy = string(pod.Spec.RestartPolicy)

	// get containers
	containers, cpu, mem, err := p.getContainers(pod, false)
	if err != nil {
		return err
	}
	initContainers, icpu, imem, err := p.getContainers(pod, true)
	if err != nil {
		return err
	}
	volumes, err := p.getVolumes(pod)
	if err != nil {
		return err
	}

	// get registry creds
	creds, err := p.getImagePullSecrets(pod)
	if err != nil {
		return err
	}

	// assign all the things
	request.NodeName = NodeName
	request.NodeId = NodeId

	request.Namespace = pod.Namespace
	request.ClusterId = ClusterId
	request.SiteId = SiteId

	request.Container = containers
	request.InitContainer = initContainers
	request.Volumes = volumes
	request.ImageRegistryCredentials = creds
	request.PrivateId = PrivateId
	request.OwnerReferences = ownerMap

	request.ContainerGroupName = fmt.Sprintf("%s-%s", pod.Namespace, pod.Name)
	request.PodName = pod.Name
	request.CreationTimestamp = pod.CreationTimestamp.UTC().Format(podTagTimeFormat)

	request.Cpu, request.Memory = cpu+icpu, mem+imem
	request.StorageType, request.StorageSize = makeStorageType(pod)

	log.G(ctx).WithField("CDS", "CreatePod").Debug(fmt.Sprintf("create pod: %v, %v, %v, %v",
		pod.Namespace, pod.Name, pod.Status.Phase, pod.Status.Reason))

	cckRequest, _ := cdsapi.NewCCKRequest(ctx, CreateContainerGroupAction, http.MethodPost, nil, request)
	response, err := cdsapi.DoOpenApiRequest(ctx, cckRequest, 0)
	if err != nil {
		log.G(ctx).WithField("Action", CreateContainerGroupAction).Error(err)
		return err
	}
	_, err = cdsapi.CdsRespDeal(ctx, response, CreateContainerGroupAction, nil)
	if err != nil {
		log.G(ctx).WithField("CDS", "CreatePod").Error(fmt.Sprintf("%s-%s: %v", pod.Namespace, pod.Name, err))

		return err
	}

	return nil
}

// UpdatePod Update Annotations
func (p *ECIProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithField("CDS", "UpdatePod").Debug(
		fmt.Sprintf("update pod: %v, %v, %v, %v", pod.Name, pod.Namespace, pod.Status.Phase, pod.Status.Reason))

	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["cluster-id"] = ClusterId
	pod.Annotations["virtual-node-id"] = NodeId
	pod.Annotations["eci-private-id"] = PrivateId

	if pod.Annotations["eci-instance-id"] == "" || pod.Annotations["eci-task-id"] == "" {
		cgs, _, _ := p.GetCgs(ctx, pod.Namespace, pod.Name)
		eciId := ""
		cpu := ""
		mem := ""
		taskId := ""
		if len(cgs) == 1 {
			eciId = cgs[0].ContainerGroupId
			cpu = fmt.Sprintf("%.2f", cgs[0].Cpu)
			mem = fmt.Sprintf("%.2f", cgs[0].Memory)
			taskId = cgs[0].TaskId
		} else if len(cgs) > 1 {
			for _, v := range cgs {
				if v.ContainerGroupName == fmt.Sprintf("%v-%v", pod.Namespace, pod.Name) {
					eciId = v.ContainerGroupId
					taskId = v.TaskId
					cpu = fmt.Sprintf("%.2f", v.Cpu)
					mem = fmt.Sprintf("%.2f", v.Memory)
				}
			}
		}
		pod.Annotations["eci-instance-id"] = eciId
		pod.Annotations["eci-instance-cpu"] = cpu
		pod.Annotations["eci-instance-mem"] = mem
		pod.Annotations["eci-task-id"] = taskId
	}
	return nil
}

// DeletePod deletes the specified pod out of ECI.
func (p *ECIProvider) DeletePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithField("CDS", "DeletePod").Debug(
		fmt.Sprintf("delete pod: %v %v %v %v", pod.Name, pod.Namespace, pod.Status.Phase, pod.Status.Reason))
	eciId := ""
	if pod.Annotations != nil {
		eciId = pod.Annotations["eci-instance-id"]
	}
	if eciId == "" {
		cgs, code, err := p.GetCgs(ctx, pod.Namespace, pod.Name)
		if err != nil || code >= 400 {
			log.G(ctx).WithField("CDS", "DeletePod").Debug(
				fmt.Sprintf("get cg error: %v %v", code, err))
		}
		if len(cgs) == 1 {
			eciId = cgs[0].ContainerGroupId
		} else if len(cgs) > 1 {
			for _, v := range cgs {
				if v.ContainerGroupName == fmt.Sprintf("%v-%v", pod.Namespace, pod.Name) {
					eciId = v.ContainerGroupId
				}
			}
		}
	}
	if eciId == "" {
		log.G(ctx).WithField("CDS", "DeletePod").Error(
			fmt.Sprintf("can't find Pod %s id", pod.Name))
		return errdefs.NotFoundf(" can't find Pod %s", pod.Name)
	}
	cckRequest, _ := cdsapi.NewCCKRequest(ctx, DeleteContainerGroupAction, http.MethodPost, nil,
		DeleteContainerGroup{ContainerGroupId: eciId})
	response, err := cdsapi.DoOpenApiRequest(ctx, cckRequest, 0)
	if err != nil {
		log.G(ctx).WithField("Action", DeleteContainerGroupAction).Error(err)
		if response != nil {
			if response.StatusCode >= 400 && response.StatusCode < 500 {
				return nil
			}
		}
		return err
	}
	content, _ := io.ReadAll(response.Body)
	log.G(ctx).WithField("Action", DeleteContainerGroupAction).Debug(string(content))
	return nil
}

func (p *ECIProvider) GetPod(ctx context.Context, namespace, name string) (*v1.Pod, error) {
	if strings.Contains(name, "disk-csi-cds-node") ||
		strings.Contains(name, "nas-csi-cds-node") ||
		strings.Contains(name, "oss-csi-cds-node") {
		return nil, nil
	}
	pod, err := p.GetPodByCondition(ctx, "K8s-GetPod", namespace, name)
	if err != nil {
		log.G(ctx).WithField("CDS", "GetPod").Error("get pod err: ", err)
		return nil, err
	}
	return pod, nil
}

// GetContainerLogs returns the logs of a pod by name that is running inside ECI.
func (p *ECIProvider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	logContent := "todo "
	eciId := ""
	cgs, _, _ := p.GetCgs(ctx, namespace, podName)
	if len(cgs) == 1 {
		eciId = cgs[0].ContainerGroupId
	}
	if eciId == "" {
		return nil, errdefs.NotFoundf("GetContainerLogs can't find Pod %s", podName)
	}
	logContent += eciId
	return io.NopCloser(strings.NewReader(logContent)), nil
}

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *ECIProvider) RunInContainer(ctx context.Context, namespace, podName, containerName string, cmd []string, attach api.AttachIO) error {
	return nil
}

// GetPodStatus returns the status of a pod by name that is running inside ECI
// returns nil if a pod by that name is not found.
func (p *ECIProvider) GetPodStatus(ctx context.Context, namespace, name string) (*v1.PodStatus, error) {
	if strings.Contains(name, "disk-csi-cds-node") ||
		strings.Contains(name, "nas-csi-cds-node") ||
		strings.Contains(name, "oss-csi-cds-node") {
		return nil, fmt.Errorf("invalid pod")
	}
	pod, err := p.GetPodByCondition(ctx, "Provider-GetPodStatus", namespace, name)
	if err != nil || pod == nil {
		log.G(ctx).WithField("CDS", "GetPodStatus").Error(fmt.Sprintf("%s-%s status err: %s", namespace, name, err))
		return nil, fmt.Errorf("err:%v, pod: %v", err, pod)

	}
	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be running within ECI.
func (p *ECIProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	pods := make([]*v1.Pod, 0)
	cgs, _, err := p.GetCgs(ctx, "", "")
	if err != nil {
		return nil, err
	}
	for _, cg := range cgs {
		c := cg
		pod, err := containerGroupToPod(&c)
		if err != nil {
			msg := fmt.Sprint("error converting container group to pod", cg.ContainerGroupId, err)
			log.G(context.TODO()).WithField("Func", "GetPods").Error(msg)
			continue
		}
		pods = append(pods, pod)
	}
	return pods, nil
}

// Capacity returns a resource list containing the capacity limits set for ECI.
func (p *ECIProvider) Capacity(ctx context.Context) v1.ResourceList {
	return v1.ResourceList{
		"cpu":               resource.MustParse(p.cpu),
		"memory":            resource.MustParse(p.memory),
		"pods":              resource.MustParse(p.maxPods),
		"ephemeral-storage": resource.MustParse("40Ti"),
	}
}

// NodeConditions returns a list of conditions (Ready, OutOfDisk, etc), for updates to the node status
// within Kubernetes.
func (p *ECIProvider) NodeConditions(ctx context.Context) []v1.NodeCondition {
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletReady",
			Message:            "kubelet is ready.",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}
}

// NodeAddresses returns a list of addresses for the node status
// within Kubernetes.
func (p *ECIProvider) NodeAddresses(ctx context.Context) []v1.NodeAddress {
	return []v1.NodeAddress{
		{
			Type:    "InternalIP",
			Address: p.internalIP,
		},
	}
}

// NodeDaemonEndpoints returns NodeDaemonEndpoints for the node status
// within Kubernetes.
func (p *ECIProvider) NodeDaemonEndpoints(ctx context.Context) *v1.NodeDaemonEndpoints {
	return &v1.NodeDaemonEndpoints{
		KubeletEndpoint: v1.DaemonEndpoint{
			Port: p.daemonEndpointPort,
		},
	}
}

// OperatingSystem returns the operating system that was provided by the config.
func (p *ECIProvider) OperatingSystem() string {
	return p.operatingSystem
}
