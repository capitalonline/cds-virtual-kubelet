package eci

type ContainerGroupResp struct {
	Eci []ContainerGroup `json:"eci"`
}

type ContainerGroup struct {
	ContainerGroupId   string          `json:"container_group_id"`
	ContainerGroupName string          `json:"container_group_name"`
	PodName            string          `json:"pod_name"`
	Namespace          string          `json:"namespace"`
	SiteId             string          `json:"site_id"`
	Memory             float64         `json:"memory"`
	Cpu                float64         `json:"cpu"`
	PrivateId          string          `json:"private_id"`
	RestartPolicy      string          `json:"restart_policy"`
	IntranetIp         string          `json:"intranet_ip"`
	Status             string          `json:"status"`
	CreationTime       string          `json:"creation_time"`
	SucceededTime      string          `json:"succeeded_time"`
	Volumes            []Volume        `json:"volumes"`
	Events             []Event         `json:"events" `
	Containers         []ContainerInfo `json:"containers"`
}

type DescribeContainerGroupsRequest struct {
	SiteId             string `json:"site_id"`
	Limit              int    `json:"limit,omitempty"`
	NodeName           string `json:"node_name,omitempty"`
	NodeId             string `json:"node_id,omitempty"`
	ContainerGroupName string `json:"container_group_name,omitempty"`
	ContainerGroupId   string `json:"container_group_id,omitempty"`
	Namespace          string `json:"namespace,omitempty"`
}
