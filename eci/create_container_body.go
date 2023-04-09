package eci

type CreateContainerGroup struct {
	SiteId                     string                    `json:"site_id"`
	ClusterId                  string                    `json:"cluster_id"`
	NodeId                     string                    `json:"node_id"`
	NodeName                   string                    `json:"node_name,omitempty"`
	Namespace                  string                    `json:"namespace"`
	BillMethod                 int                       `json:"bill_method"`
	OwnerReferences            interface{}               `json:"owner_references"`
	ContainerGroupName         string                    `json:"name"`
	ContainerGroupInstanceType string                    `json:"container_groupInstance_type,omitempty"`
	PodName                    string                    `json:"pod_name"`
	Cpu                        float64                   `json:"cpu"`
	Memory                     float64                   `json:"memory"`
	RestartPolicy              string                    `json:"restart_policy"`
	Amount                     int                       `json:"amount,omitempty"`
	StorageType                string                    `json:"ephemeral_storage_type"`
	StorageSize                int                       `json:"ephemeral_storage_size"`
	PublicIp                   []string                  `json:"public_ip,omitempty"`
	PrivateId                  string                    `json:"private_pipe_id"`
	Container                  []ContainerInfo           `json:"container"`
	InitContainer              []ContainerInfo           `json:"init_container"`
	Volumes                    []Volume                  `json:"volumes"`
	ImageRegistryCredentials   []ImageRegistryCredential `json:"image_registry_credential"`
	CreationTimestamp          string                    `json:"creation_timestamp"`
}
