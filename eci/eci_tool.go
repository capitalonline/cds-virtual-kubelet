package eci

import (
	"encoding/json"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"strconv"
	"time"
)

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
			Annotations: map[string]string{
				"eci-instance-id": cg.ContainerGroupId,
			},
		},
		Spec: v1.PodSpec{
			NodeName:   NodeName,
			Volumes:    []v1.Volume{},
			Containers: containers,
		},
		Status: v1.PodStatus{
			Phase:             eciStateToPodPhase(eciState),
			Conditions:        eciStateToPodConditions(eciState, podCreationTimestamp),
			Message:           cg.TaskState,
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
		return v1.PodPhase("Scheduling")
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
