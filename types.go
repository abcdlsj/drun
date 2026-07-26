package main

type containerInfo struct {
	ID     string          `json:"Id"`
	Image  string          `json:"Image"`
	Name   string          `json:"Name"`
	Config containerConfig `json:"Config"`
	State  struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
	HostConfig struct {
		AutoRemove      bool                     `json:"AutoRemove"`
		Binds           []string                 `json:"Binds"`
		CapAdd          []string                 `json:"CapAdd"`
		CapDrop         []string                 `json:"CapDrop"`
		DNS             []string                 `json:"Dns"`
		ExtraHosts      []string                 `json:"ExtraHosts"`
		GroupAdd        []string                 `json:"GroupAdd"`
		Init            *bool                    `json:"Init"`
		NetworkMode     string                   `json:"NetworkMode"`
		PortBindings    map[string][]portBinding `json:"PortBindings"`
		Privileged      bool                     `json:"Privileged"`
		PublishAllPorts bool                     `json:"PublishAllPorts"`
		ReadonlyRootfs  bool                     `json:"ReadonlyRootfs"`
		RestartPolicy   restartPolicy            `json:"RestartPolicy"`
		SecurityOpt     []string                 `json:"SecurityOpt"`
		ShmSize         int64                    `json:"ShmSize"`
		Tmpfs           map[string]string        `json:"Tmpfs"`
	} `json:"HostConfig"`
	Mounts          []mountInfo `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]networkInfo `json:"Networks"`
	} `json:"NetworkSettings"`
}

type imageInfo struct {
	ID       string          `json:"Id"`
	RepoTags []string        `json:"RepoTags"`
	Config   containerConfig `json:"Config"`
}

type containerConfig struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd"`
	Entrypoint []string          `json:"Entrypoint"`
	Env        []string          `json:"Env"`
	Labels     map[string]string `json:"Labels"`
	User       string            `json:"User"`
	WorkingDir string            `json:"WorkingDir"`
}

type updateCandidate struct {
	Name           string
	Image          string
	CurrentImageID string
	LatestImageID  string
	CurrentVersion string
	LatestVersion  string
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
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type networkInfo struct {
	Aliases []string `json:"Aliases"`
}
