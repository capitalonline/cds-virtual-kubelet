package eci

type ContainerInfo struct {
	Id              string           `json:"id,omitempty"`
	Name            string           `json:"name"`
	Image           string           `json:"image"`
	ImageVersion    string           `json:"version"`
	ImagePullPolicy string           `json:"image_pull_policy"`
	WorkingDir      string           `json:"working_dir"`
	Arg             []string         `json:"arg"`
	Command         []string         `json:"command"`
	Memory          float64          `json:"memory"`
	Cpu             float64          `json:"cpu"`
	Ports           []ContainerPort  `json:"ports"`
	EnvironmentVars []EnvironmentVar `json:"environment_var"`
	VolumeMounts    []VolumeMount    `json:"volume_mounts"`
	RestartCount    int              `json:"restart_count,omitempty"`
	PreviousState   *ContainerState  `json:"previous_state,omitempty"`
	CurrentState    *ContainerState  `json:"current_state,omitempty"`
}

type ContainerState struct {
	State        string `json:"state"`
	DetailStatus string `json:"detail_status"`
	ExitCode     int    `json:"exit_code"`
	StartTime    string `json:"start_time"`
	FinishTime   string `json:"finish_time"`
}

type ImageRegistryCredential struct {
	Server   string `json:"server"`
	UserName string `json:"user_name"`
	Password string `json:"password"`
}

type EnvironmentVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ContainerPort struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

type VolumeMount struct {
	MountPath string `json:"mount_path"`
	ReadOnly  bool   `json:"read_only"`
	Name      string `json:"name"`
}

type Event struct {
	Count          int    `json:"count"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	Message        string `json:"message"`
	FirstTimestamp string `json:"first_timestamp"`
	LastTimestamp  string `json:"last_timestamp"`
}

const (
	VOL_TYPE_NFS              = "NFSVolume"
	VOL_TYPE_EMPTYDIR         = "EmptyDirVolume"
	VOL_TYPE_CONFIGFILEVOLUME = "ConfigFileVolume"
)

type Volume struct {
	Type                 string             `json:"type"`
	Name                 string             `json:"name"`
	NfsVolumePath        string             `json:"nfs_volume_path"`
	NfsVolumeServer      string             `json:"nfs_volume_server"`
	NfsVolumeReadOnly    bool               `json:"nfs_volume_read_only"`
	EmptyDirVolumeEnable bool               `json:"empty_dir_volume_enable"`
	ConfigFileToPaths    []ConfigFileToPath `json:"config_file_to_paths"`
}

type ConfigFileToPath struct {
	Content string `json:"content"`
	Path    string `json:"path"`
}
