package eci

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/capitalonline/cds-virtual-kubelet/cdsapi"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"strings"
	"time"
)

const podTagTimeFormat = "2006-01-02T15-04-05Z"
const timeFormat = "2006-01-02T15:04:05Z"

func (p *ECIProvider) GetPodByCondition(ctx context.Context, source, namespace, name string) (*v1.Pod, error) {
	log.G(ctx).WithField("CDS", "GetPodByCondition").Warn(source+": get cds eci: ", name+"-"+namespace)
	cgs, code, err := p.GetCgs(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	if code >= 500 {
		return nil, err
	} else {
		if len(cgs) == 1 {
			cg := cgs[0]
			return containerGroupToPod(&cg)
		} else if len(cgs) > 1 {
			log.G(ctx).WithField("CDS", "GetPodByCondition").Warn(source+": get pod is non-uniqueness: ", name+" "+namespace)
			return nil, nil
		} else {
			log.G(ctx).WithField("CDS", "GetPodByCondition").Debug(source+": get pod is null ", name+" "+namespace)
			return nil, nil
		}
	}
}

func (p *ECIProvider) GetCgs(ctx context.Context, namespace, name string) ([]ContainerGroup, int, error) {
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
	response, err := cdsapi.DoOpenApiRequest(ctx, cckRequest, 0)
	if err != nil {
		return nil, 0, err
	}
	code, err := cdsapi.CdsRespDeal(ctx, response, DescribeContainerGroups, &cgs)
	if err != nil {
		log.G(ctx).WithField("CDS", "GetCgs").Error(err)
		return nil, code, err
	}
	return cgs.Eci, response.StatusCode, nil
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
		if init {
			cpuRequest = 0
		}
		if _, ok := container.Resources.Limits[v1.ResourceCPU]; ok {
			cpuRequest = float64(container.Resources.Limits.Cpu().MilliValue()) / 1000.00
		}
		c.Cpu = cpuRequest

		memoryRequest := 2.0
		if init {
			memoryRequest = 0
		}
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

func (p *ECIProvider) temporaryPod(namespace, name string) *v1.Pod {
	var (
		containerStartTime = metav1.NewTime(time.Now())
		containerStatuses  []v1.ContainerStatus
		podStat            = v1.PodPending
		containerReason    = "Scheduling"
	)

	containerStatus := v1.ContainerStatus{
		Name: name,
		State: v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason:  containerReason,
				Message: "get status 50X",
			},
		},
		LastTerminationState: v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason:  containerReason,
				Message: "get status 50X",
			},
		},
		Ready:        false,
		RestartCount: 0,
		Image:        "",
		ImageID:      "",
		ContainerID:  "",
	}

	// Add to containerStatuses
	containerStatuses = append(containerStatuses, containerStatus)

	pod := v1.Pod{
		Status: v1.PodStatus{
			Phase:             podStat,
			Conditions:        nil,
			Message:           "get status 500",
			Reason:            "",
			HostIP:            "",
			PodIP:             "",
			StartTime:         &containerStartTime,
			ContainerStatuses: containerStatuses,
		},
	}
	return &pod
}
