package main

type containerInfo struct {
	ID         string          `json:"Id"`
	Image      string          `json:"Image"`
	Name       string          `json:"Name"`
	Config     containerConfig `json:"Config"`
	State      containerState  `json:"State"`
	HostConfig hostConfig      `json:"HostConfig"`
	Mounts     []mountInfo     `json:"Mounts"`

	NetworkSettings struct {
		Networks map[string]networkInfo `json:"Networks"`
	} `json:"NetworkSettings"`
}

type containerState struct {
	Running bool   `json:"Running"`
	Status  string `json:"Status"`
}

type hostConfig struct {
	AutoRemove        bool                     `json:"AutoRemove"`
	Binds             []string                 `json:"Binds"`
	CapAdd            []string                 `json:"CapAdd"`
	CapDrop           []string                 `json:"CapDrop"`
	CgroupParent      string                   `json:"CgroupParent"`
	CPUPeriod         int64                    `json:"CpuPeriod"`
	CPUQuota          int64                    `json:"CpuQuota"`
	CPUShares         int64                    `json:"CpuShares"`
	CpusetCpus        string                   `json:"CpusetCpus"`
	CpusetMems        string                   `json:"CpusetMems"`
	DeviceRequests    []deviceRequest          `json:"DeviceRequests"`
	Devices           []deviceMapping          `json:"Devices"`
	DNS               []string                 `json:"Dns"`
	ExtraHosts        []string                 `json:"ExtraHosts"`
	GroupAdd          []string                 `json:"GroupAdd"`
	Init              *bool                    `json:"Init"`
	IpcMode           string                   `json:"IpcMode"`
	LogConfig         logConfig                `json:"LogConfig"`
	Memory            int64                    `json:"Memory"`
	MemoryReservation int64                    `json:"MemoryReservation"`
	MemorySwap        int64                    `json:"MemorySwap"`
	Mounts            []mountSpec              `json:"Mounts"`
	NanoCPUs          int64                    `json:"NanoCpus"`
	NetworkMode       string                   `json:"NetworkMode"`
	OomKillDisable    *bool                    `json:"OomKillDisable"`
	OomScoreAdj       int                      `json:"OomScoreAdj"`
	PidMode           string                   `json:"PidMode"`
	PidsLimit         *int64                   `json:"PidsLimit"`
	PortBindings      map[string][]portBinding `json:"PortBindings"`
	Privileged        bool                     `json:"Privileged"`
	PublishAllPorts   bool                     `json:"PublishAllPorts"`
	ReadonlyRootfs    bool                     `json:"ReadonlyRootfs"`
	RestartPolicy     restartPolicy            `json:"RestartPolicy"`
	Runtime           string                   `json:"Runtime"`
	SecurityOpt       []string                 `json:"SecurityOpt"`
	ShmSize           int64                    `json:"ShmSize"`
	Sysctls           map[string]string        `json:"Sysctls"`
	Tmpfs             map[string]string        `json:"Tmpfs"`
	Ulimits           []ulimit                 `json:"Ulimits"`
	UTSMode           string                   `json:"UTSMode"`
}

type imageInfo struct {
	ID          string          `json:"Id"`
	RepoTags    []string        `json:"RepoTags"`
	RepoDigests []string        `json:"RepoDigests"`
	Config      containerConfig `json:"Config"`
}

type containerConfig struct {
	Image        string              `json:"Image"`
	Cmd          []string            `json:"Cmd"`
	Entrypoint   []string            `json:"Entrypoint"`
	Env          []string            `json:"Env"`
	Labels       map[string]string   `json:"Labels"`
	User         string              `json:"User"`
	WorkingDir   string              `json:"WorkingDir"`
	Hostname     string              `json:"Hostname"`
	Domainname   string              `json:"Domainname"`
	Tty          bool                `json:"Tty"`
	OpenStdin    bool                `json:"OpenStdin"`
	StopSignal   string              `json:"StopSignal"`
	StopTimeout  *int                `json:"StopTimeout"`
	Healthcheck  *healthConfig       `json:"Healthcheck"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts"`
}

type healthConfig struct {
	Test        []string `json:"Test"`
	Interval    int64    `json:"Interval"`
	Timeout     int64    `json:"Timeout"`
	StartPeriod int64    `json:"StartPeriod"`
	Retries     int      `json:"Retries"`
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type restartPolicy struct {
	Name              string `json:"Name"`
	MaximumRetryCount int    `json:"MaximumRetryCount"`
}

type mountInfo struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

// mountSpec is a mount created with --mount, as recorded in HostConfig.Mounts.
type mountSpec struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Target      string `json:"Target"`
	ReadOnly    bool   `json:"ReadOnly"`
	BindOptions *struct {
		Propagation string `json:"Propagation"`
	} `json:"BindOptions"`
	VolumeOptions *struct {
		NoCopy bool `json:"NoCopy"`
	} `json:"VolumeOptions"`
	TmpfsOptions *struct {
		SizeBytes int64 `json:"SizeBytes"`
		Mode      int64 `json:"Mode"`
	} `json:"TmpfsOptions"`
}

type networkInfo struct {
	Aliases    []string `json:"Aliases"`
	IPAMConfig *struct {
		IPv4Address string `json:"IPv4Address"`
		IPv6Address string `json:"IPv6Address"`
	} `json:"IPAMConfig"`
}

type logConfig struct {
	Type   string            `json:"Type"`
	Config map[string]string `json:"Config"`
}

type deviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

type deviceRequest struct {
	Driver       string     `json:"Driver"`
	Count        int        `json:"Count"`
	DeviceIDs    []string   `json:"DeviceIDs"`
	Capabilities [][]string `json:"Capabilities"`
}

type ulimit struct {
	Name string `json:"Name"`
	Soft int64  `json:"Soft"`
	Hard int64  `json:"Hard"`
}
