package eci

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/capitalonline/cds-virtual-kubelet/cdsapi"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/manager"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"io"
	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const podTagTimeFormat = "2006-01-02T15-04-05Z"
const timeFormat = "2006-01-02T15:04:05Z"

// ECIProvider implements the virtual-kubelet provider interface and communicates with Alibaba Cloud's ECI APIs.
type ECIProvider struct {
	resourceManager    *manager.ResourceManager
	nodeName           string
	operatingSystem    string
	cpu                string
	memory             string
	pods               string
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

	p.cpu = "1000"
	p.memory = "4Ti"
	p.pods = MaxPods

	p.operatingSystem = operatingSystem
	p.nodeName = nodeName
	p.internalIP = internalIP
	p.daemonEndpointPort = daemonEndpointPort
	return &p, err
}

// CreatePod accepts a Pod definition and creates an ECI deployment
func (p *ECIProvider) CreatePod(ctx context.Context, pod *v1.Pod) error {
	// 忽略 daemonSet Pod
	if pod != nil && pod.OwnerReferences != nil && len(pod.OwnerReferences) != 0 && pod.OwnerReferences[0].Kind == "DaemonSet" {
		return fmt.Errorf("%s DaemonSet unsupported", pod.Name)
	}
	if pod.Status.Reason == "ProviderFailed" {
		return fmt.Errorf("%s", pod.Status.Message)
	}
	var (
		ownerMap = make(map[string]string)
	)
	if pod != nil && pod.OwnerReferences != nil && len(pod.OwnerReferences) != 0 {
		ownerMap["kind"] = pod.OwnerReferences[0].Kind
		ownerMap["name"] = pod.OwnerReferences[0].Name
	}
	log.G(ctx).WithField("CDS", "cds-debug").Debug(fmt.Sprintf("create pod: %v, %v, %v, %v, %v",
		pod.Namespace, pod.Name, pod.Status.Phase, pod.Status.Reason, pod.Status.Message))

	request := CreateContainerGroup{}
	request.RestartPolicy = string(pod.Spec.RestartPolicy)

	// get containers
	containers, cpu, mem, err := p.getContainers(pod, false)
	if err != nil {
		return err
	}
	initContainers, _, _, err := p.getContainers(pod, true)
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

	request.Cpu, request.Memory = cpu, mem
	request.StorageType, request.StorageSize = makeStorageType(pod)

	cckRequest, _ := cdsapi.NewCCKRequest(ctx, CreateContainerGroupAction, http.MethodPost, nil, request)
	response, err := cdsapi.DoOpenApiRequest(ctx, cckRequest, 0)
	if err != nil {
		log.G(ctx).WithField("Action", CreateContainerGroupAction).Error(err)
		return err
	}
	code, msg, err := cdsapi.CdsRespDeal(ctx, response, nil)
	log.G(ctx).WithField("CDS", "cds-debug").Debug(fmt.Sprintf("create pod resp stat: %v, %v", code, msg))
	if err != nil {
		log.G(ctx).WithField("Func", "CreatePod").Error(err)
		return err
	} else if code == "CreateEciTaskError" {
		return fmt.Errorf("%v", code)
	} else if code == "MaxPodError" {
		return fmt.Errorf("%v", code)
	}
	return nil
}

// UpdatePod Update Annotations
func (p *ECIProvider) UpdatePod(ctx context.Context, pod *v1.Pod) error {
	log.G(ctx).WithField("CDS", "cds-debug").Debug(fmt.Sprintf("update pod: %v %v %v %v", pod.Name, pod.Namespace, pod.Status.Phase, pod.Status.Reason))
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["cluster-id"] = ClusterId
	pod.Annotations["virtual-node-id"] = NodeId
	pod.Annotations["eci-private_id"] = PrivateId
	if pod.Annotations["eci-instance-id"] == "" {
		cgs := p.GetCgs(ctx, pod.Namespace, pod.Name)
		eciId := ""
		cpu := ""
		mem := ""
		if len(cgs) == 1 {
			eciId = cgs[0].ContainerGroupId
			cpu = fmt.Sprintf("%.1f", cgs[0].Cpu)
			mem = fmt.Sprintf("%.1f", cgs[0].Memory)
		} else if len(cgs) > 1 {
			for _, v := range cgs {
				if v.ContainerGroupName == fmt.Sprintf("%v-%v", pod.Namespace, pod.Name) {
					eciId = v.ContainerGroupId
					cpu = fmt.Sprintf("%.1f", v.Cpu)
					mem = fmt.Sprintf("%.1f", v.Memory)
				}
			}
		}
		pod.Annotations["eci-instance-id"] = eciId
		pod.Annotations["eci-instance-cpu"] = cpu
		pod.Annotations["eci-instance-mem"] = mem
	}
	return nil
}

// DeletePod deletes the specified pod out of ECI.
func (p *ECIProvider) DeletePod(ctx context.Context, pod *v1.Pod) error {
	eciId := ""
	cgs := p.GetCgs(ctx, pod.Namespace, pod.Name)
	log.G(ctx).WithField("CDS", "cds-debug").Debug(fmt.Sprintf("delete pod: %v %v %v %v", pod.Name, pod.Namespace, pod.Status.Phase, pod.Status.Reason))
	if len(cgs) == 1 {
		eciId = cgs[0].ContainerGroupId
	} else if len(cgs) > 1 {
		for _, v := range cgs {
			if v.ContainerGroupName == fmt.Sprintf("%v-%v", pod.Namespace, pod.Name) {
				eciId = v.ContainerGroupId
			}
		}
	}
	if eciId == "" {
		log.G(ctx).WithField("CDS", "cds-debug").Debug(fmt.Sprintf("delete pod fail: %v %v %v %v", pod.Name, pod.Namespace, pod.Status.Phase, pod.Status.Reason))
		return errdefs.NotFoundf(" can't find Pod %s", pod.Name)
	}

	cckRequest, _ := cdsapi.NewCCKRequest(ctx, DeleteContainerGroupAction, http.MethodPost, nil,
		DeleteContainerGroup{ContainerGroupId: eciId})
	response, err := cdsapi.DoOpenApiRequest(ctx, cckRequest, 0)
	if err != nil {
		log.G(ctx).WithField("Action", DeleteContainerGroupAction).Error(err)
		return err
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	log.G(ctx).WithField("Action", DeleteContainerGroupAction).Debug(string(content))
	return nil
}

func (p *ECIProvider) GetPod(ctx context.Context, namespace, name string) (*v1.Pod, error) {
	if strings.Contains(name, "disk-csi-cds-node") {
		return nil, nil
	}
	if strings.Contains(name, "nas-csi-cds-node") {
		return nil, nil
	}
	if strings.Contains(name, "oss-csi-cds-node") {
		return nil, nil
	}
	log.G(ctx).WithField("CDS", "cds-debug").Debug("get pod: ", name+" "+namespace)
	pod, err := p.GetPodByCondition(ctx, namespace, name)
	if err != nil {
		log.G(context.TODO()).WithField("Func", "GetPod").Error(err)
		return nil, err
	}
	return pod, nil
}

// GetContainerLogs returns the logs of a pod by name that is running inside ECI.
func (p *ECIProvider) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	logContent := "todo "
	eciId := ""
	cgs := p.GetCgs(ctx, namespace, podName)
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
	pod, err := p.GetPod(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	if pod == nil {
		return nil, nil
	}
	return &pod.Status, nil
}

// GetPods returns a list of all pods known to be running within ECI.
func (p *ECIProvider) GetPods(ctx context.Context) ([]*v1.Pod, error) {
	pods := make([]*v1.Pod, 0)
	for _, cg := range p.GetCgs(ctx, "", "") {
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

func (p *ECIProvider) GetPodByCondition(ctx context.Context, namespace, name string) (*v1.Pod, error) {
	// 根据 nodeId+ns+podName 精确查询
	cgs := p.GetCgs(ctx, namespace, name)
	if len(cgs) == 1 {
		cg := cgs[0]
		return containerGroupToPod(&cg)
	} else if len(cgs) > 1 {
		log.G(ctx).WithField("CDS", "cds-debug").Debug("get pod by condition warn: non-uniqueness: ", name+" "+namespace)
		return nil, nil
	} else {
		return nil, nil
	}
}

func (p *ECIProvider) GetCgs(ctx context.Context, namespace, name string) []ContainerGroup {
	var cname string
	if namespace != "" && name != "" {
		cname = fmt.Sprintf("%s-%s", namespace, name)
	}
	cgs := ContainerGroupResp{}
	request := DescribeContainerGroupsRequest{
		SiteId:             SiteId,
		NodeId:             NodeId,
		Namespace:          namespace,
		ContainerGroupName: cname,
	}
	cckRequest, _ := cdsapi.NewCCKRequest(ctx, DescribeContainerGroups, http.MethodPost, nil, request)
	response, err := cdsapi.DoOpenApiRequest(ctx, cckRequest, 3000)
	if err != nil {
		log.G(ctx).WithField("Func", "GetCgs").Error(err)
		return nil
	}
	_, _, err = cdsapi.CdsRespDeal(ctx, response, &cgs)
	if err != nil {
		log.G(ctx).WithField("Func", "GetCgs").Error(err)
		return nil
	}
	return cgs.Eci
}

// Capacity returns a resource list containing the capacity limits set for ECI.
func (p *ECIProvider) Capacity(ctx context.Context) v1.ResourceList {
	return v1.ResourceList{
		"cpu":    resource.MustParse(p.cpu),
		"memory": resource.MustParse(p.memory),
		"pods":   resource.MustParse(p.pods),
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

func (p *ECIProvider) getContainers(pod *v1.Pod, init bool) ([]ContainerInfo, float64, float64, error) {
	var (
		allCpu float64
		allMem float64
	)
	podContainers := pod.Spec.Containers
	if init {
		podContainers = pod.Spec.InitContainers
	}
	containers := make([]ContainerInfo, 0, len(podContainers))
	for _, container := range podContainers {
		imageList := strings.Split(container.Image, ":")
		imageName := ""
		imageVersion := ""
		if len(imageList) > 1 {
			imageName = imageList[0]
			imageVersion = imageList[1]
		} else {
			imageName = container.Image

		}
		if imageVersion == "" {
			imageVersion = "latest"
		}
		c := ContainerInfo{

			Name:         container.Name,
			Image:        imageName,
			ImageVersion: imageVersion,
			Command:      append(container.Command, container.Args...),
			Ports:        make([]ContainerPort, 0, len(container.Ports)),
		}

		for _, port := range container.Ports {
			c.Ports = append(c.Ports, ContainerPort{
				Port:     int(port.ContainerPort),
				Protocol: string(port.Protocol),
			})
		}

		c.VolumeMounts = make([]VolumeMount, 0, len(container.VolumeMounts))
		for _, v := range container.VolumeMounts {
			c.VolumeMounts = append(c.VolumeMounts, VolumeMount{
				Name:      v.Name,
				MountPath: v.MountPath,
				ReadOnly:  v.ReadOnly,
			})
		}

		c.EnvironmentVars = make([]EnvironmentVar, 0, len(container.Env))
		for _, e := range container.Env {
			c.EnvironmentVars = append(c.EnvironmentVars, EnvironmentVar{Key: e.Name, Value: e.Value})
		}

		cpuRequest := 1.00
		if _, ok := container.Resources.Limits[v1.ResourceCPU]; ok {
			cpuRequest = float64(container.Resources.Limits.Cpu().MilliValue()) / 1000.00
		}

		c.Cpu = cpuRequest

		memoryRequest := 2.0
		if _, ok := container.Resources.Limits[v1.ResourceMemory]; ok {
			memoryRequest = float64(container.Resources.Limits.Memory().Value()) / 1024.0 / 1024.0 / 1024.0
		}

		c.Memory = memoryRequest

		c.ImagePullPolicy = string(container.ImagePullPolicy)
		c.WorkingDir = container.WorkingDir
		containers = append(containers, c)
		allCpu += c.Cpu
		allMem += c.Memory
	}
	return containers, allCpu, allMem, nil
}

func (p *ECIProvider) getImagePullSecrets(pod *v1.Pod) ([]ImageRegistryCredential, error) {
	ips := make([]ImageRegistryCredential, 0, len(pod.Spec.ImagePullSecrets))
	for _, ref := range pod.Spec.ImagePullSecrets {
		secret, err := p.resourceManager.GetSecret(ref.Name, pod.Namespace)
		if err != nil {
			return ips, err
		}
		if secret == nil {
			return nil, fmt.Errorf("error getting image pull secret")
		}
		switch secret.Type {
		case v1.SecretTypeDockercfg:
			ips, err = readDockerCfgSecret(secret, ips)
		case v1.SecretTypeDockerConfigJson:
			ips, err = readDockerConfigJSONSecret(secret, ips)
		default:
			return nil, fmt.Errorf("image pull secret type is not one of kubernetes.io/dockercfg or kubernetes.io/dockerconfigjson")
		}

		if err != nil {
			return ips, err
		}
	}
	return ips, nil
}

func readDockerCfgSecret(secret *v1.Secret, ips []ImageRegistryCredential) ([]ImageRegistryCredential, error) {
	var err error
	var authConfigs map[string]AuthConfig
	repoData, ok := secret.Data[v1.DockerConfigKey]

	if !ok {
		return ips, fmt.Errorf("no dockercfg present in secret")
	}

	err = json.Unmarshal(repoData, &authConfigs)
	if err != nil {
		return ips, fmt.Errorf("failed to unmarshal auth config %+v", err)
	}

	for server, authConfig := range authConfigs {
		ips = append(ips, ImageRegistryCredential{
			Password: authConfig.Password,
			Server:   server,
			UserName: authConfig.Username,
		})
	}

	return ips, err
}

func readDockerConfigJSONSecret(secret *v1.Secret, ips []ImageRegistryCredential) ([]ImageRegistryCredential, error) {
	var err error
	repoData, ok := secret.Data[v1.DockerConfigJsonKey]

	if !ok {
		return ips, fmt.Errorf("no dockerconfigjson present in secret")
	}

	var authConfigs map[string]map[string]AuthConfig

	err = json.Unmarshal(repoData, &authConfigs)
	if err != nil {
		return ips, err
	}

	auths, ok := authConfigs["auths"]

	if !ok {
		return ips, fmt.Errorf("malformed dockerconfigjson in secret")
	}

	for server, authConfig := range auths {
		ips = append(ips, ImageRegistryCredential{
			Password: authConfig.Password,
			Server:   server,
			UserName: authConfig.Username,
		})
	}

	return ips, err
}

func containerGroupToPod(cg *ContainerGroup) (*v1.Pod, error) {
	if cg == nil {
		return nil, nil
	}
	var podCreationTimestamp, containerStartTime metav1.Time
	CreationTimestamp := cg.CreationTime
	if CreationTimestamp != "" {
		if t, err := time.Parse(podTagTimeFormat, CreationTimestamp); err == nil {
			podCreationTimestamp = metav1.NewTime(t)
		}
	}
	if len(cg.Containers) > 0 {
		if cg.Containers[0].CurrentState != nil {
			if t, err := time.Parse(timeFormat, cg.Containers[0].CurrentState.StartTime); err == nil {
				containerStartTime = metav1.NewTime(t)
			}
		}
	}

	// Use the Provisioning State if it's not Succeeded,
	// otherwise use the state of the instance.
	eciState := cg.Status

	containers := make([]v1.Container, 0, len(cg.Containers))
	containerStatuses := make([]v1.ContainerStatus, 0, len(cg.Containers))
	for _, c := range cg.Containers {
		container := v1.Container{
			Name:    c.Name,
			Image:   c.Image,
			Command: c.Command,
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%.2f", c.Cpu)),
					v1.ResourceMemory: resource.MustParse(fmt.Sprintf("%.1fG", c.Memory)),
				},
			},
		}

		container.Resources.Limits = v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%.2f", c.Cpu)),
			v1.ResourceMemory: resource.MustParse(fmt.Sprintf("%.1fG", c.Memory)),
		}

		containers = append(containers, container)
		containerStatus := v1.ContainerStatus{
			Name:                 c.Name,
			State:                eciContainerStateToContainerState(c.CurrentState),
			LastTerminationState: eciContainerStateToContainerState(c.PreviousState),
			Ready:                eciStateToPodPhase(c.CurrentState.State) == v1.PodRunning,
			RestartCount:         int32(c.RestartCount),
			Image:                c.Image,
			ImageID:              "",
			ContainerID:          c.Id,
		}

		// Add to containerStatuses
		containerStatuses = append(containerStatuses, containerStatus)
	}

	pod := v1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              cg.PodName,
			Namespace:         cg.Namespace,
			ClusterName:       ClusterId,
			UID:               types.UID(cg.ContainerGroupId),
			CreationTimestamp: podCreationTimestamp,
		},
		Spec: v1.PodSpec{
			NodeName:   NodeName,
			Volumes:    []v1.Volume{},
			Containers: containers,
		},
		Status: v1.PodStatus{
			Phase:             eciStateToPodPhase(eciState),
			Conditions:        eciStateToPodConditions(eciState, podCreationTimestamp),
			Message:           "",
			Reason:            "",
			HostIP:            cg.IntranetIp,
			PodIP:             cg.IntranetIp,
			StartTime:         &containerStartTime,
			ContainerStatuses: containerStatuses,
		},
	}
	return &pod, nil
}

func eciStateToPodPhase(state string) v1.PodPhase {
	switch state {
	case "Scheduling":
		return v1.PodPending
	case "ScheduleFailed":
		return v1.PodFailed
	case "Pending":
		return v1.PodPending
	case "Running":
		return v1.PodRunning
	case "Failed":
		return v1.PodFailed
	case "Succeeded":
		return v1.PodSucceeded
	}
	return v1.PodUnknown
}

func eciStateToPodConditions(state string, transitionTime metav1.Time) []v1.PodCondition {
	switch state {
	case "Running", "Succeeded":
		return []v1.PodCondition{
			v1.PodCondition{
				Type:               v1.PodReady,
				Status:             v1.ConditionTrue,
				LastTransitionTime: transitionTime,
			}, v1.PodCondition{
				Type:               v1.PodInitialized,
				Status:             v1.ConditionTrue,
				LastTransitionTime: transitionTime,
			}, v1.PodCondition{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: transitionTime,
			},
		}
	}
	return []v1.PodCondition{}
}

func eciContainerStateToContainerState(cs *ContainerState) v1.ContainerState {
	if cs == nil {
		return v1.ContainerState{}
	}
	t1, err := time.Parse(timeFormat, cs.StartTime)
	if err != nil {
		return v1.ContainerState{}
	}

	startTime := metav1.NewTime(t1)

	// Handle the case where the container is running.
	if cs.State == "Running" || cs.State == "Succeeded" {
		return v1.ContainerState{
			Running: &v1.ContainerStateRunning{
				StartedAt: startTime,
			},
		}
	}

	t2, err := time.Parse(timeFormat, cs.FinishTime)
	if err != nil {
		return v1.ContainerState{}
	}

	finishTime := metav1.NewTime(t2)

	// Handle the case where the container failed.
	if cs.State == "Failed" || cs.State == "Canceled" {
		return v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode:   int32(cs.ExitCode),
				Reason:     cs.State,
				Message:    cs.DetailStatus,
				StartedAt:  startTime,
				FinishedAt: finishTime,
			},
		}
	}

	// Handle the case where the container is pending.
	// Which should be all other eci states.
	return v1.ContainerState{
		Waiting: &v1.ContainerStateWaiting{
			Reason:  cs.State,
			Message: cs.DetailStatus,
		},
	}
}

func (p *ECIProvider) getVolumes(pod *v1.Pod) ([]Volume, error) {
	volumes := make([]Volume, 0, len(pod.Spec.Volumes))
	for _, v := range pod.Spec.Volumes {
		// Handle the case for the EmptyDir.
		if v.EmptyDir != nil {
			volumes = append(volumes, Volume{
				Type:                 VOL_TYPE_EMPTYDIR,
				Name:                 v.Name,
				EmptyDirVolumeEnable: true,
			})
			continue
		}

		// Handle the case for the NFS.
		if v.NFS != nil {
			volumes = append(volumes, Volume{
				Type:              VOL_TYPE_NFS,
				Name:              v.Name,
				NfsVolumeServer:   v.NFS.Server,
				NfsVolumePath:     v.NFS.Path,
				NfsVolumeReadOnly: v.NFS.ReadOnly,
			})
			continue
		}

		// Handle the case for ConfigMap volume.
		if v.ConfigMap != nil {
			ConfigFileToPaths := make([]ConfigFileToPath, 0)
			configMap, err := p.resourceManager.GetConfigMap(v.ConfigMap.Name, pod.Namespace)
			if v.ConfigMap.Optional != nil && !*v.ConfigMap.Optional && k8serr.IsNotFound(err) {
				return nil, fmt.Errorf("ConfigMap %s is required by Pod %s and does not exist", v.ConfigMap.Name, pod.Name)
			}
			if configMap == nil {
				continue
			}

			for k, v := range configMap.Data {
				var b bytes.Buffer
				enc := base64.NewEncoder(base64.StdEncoding, &b)
				enc.Write([]byte(v))

				ConfigFileToPaths = append(ConfigFileToPaths, ConfigFileToPath{Path: k, Content: b.String()})
			}

			if len(ConfigFileToPaths) != 0 {
				volumes = append(volumes, Volume{
					Type:              VOL_TYPE_CONFIGFILEVOLUME,
					Name:              v.Name,
					ConfigFileToPaths: ConfigFileToPaths,
				})
			}
			continue
		}

		if v.Secret != nil {
			ConfigFileToPaths := make([]ConfigFileToPath, 0)
			secret, err := p.resourceManager.GetSecret(v.Secret.SecretName, pod.Namespace)
			if v.Secret.Optional != nil && !*v.Secret.Optional && k8serr.IsNotFound(err) {
				return nil, fmt.Errorf("Secret %s is required by Pod %s and does not exist", v.Secret.SecretName, pod.Name)
			}
			if secret == nil {
				continue
			}
			for k, v := range secret.Data {
				var b bytes.Buffer
				enc := base64.NewEncoder(base64.StdEncoding, &b)
				enc.Write(v)
				ConfigFileToPaths = append(ConfigFileToPaths, ConfigFileToPath{Path: k, Content: b.String()})
			}

			if len(ConfigFileToPaths) != 0 {
				volumes = append(volumes, Volume{
					Type:              VOL_TYPE_CONFIGFILEVOLUME,
					Name:              v.Name,
					ConfigFileToPaths: ConfigFileToPaths,
				})
			}
			continue
		}

		// If we've made it this far we have found a volume type that isn't supported
		return nil, fmt.Errorf("Pod %s requires volume %s which is of an unsupported type\n", pod.Name, v.Name)
	}

	return volumes, nil
}

func makeStorageType(pod *v1.Pod) (t string, size int) {
	t = pod.Annotations["eci-storage-type"]
	s := pod.Annotations["eci-storage-size"]
	if t == "" {
		t = "high_disk"
	}
	if s == "" {
		s = "20"
	}
	size, _ = strconv.Atoi(s)
	if size == 0 {
		size = 20
	}
	return t, size
}
