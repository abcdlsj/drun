package main

import (
	"fmt"
	"strings"
)

const (
	composeProjectLabel         = "com.docker.compose.project"
	composeServiceLabel         = "com.docker.compose.service"
	composeWorkingDirLabel      = "com.docker.compose.project.working_dir"
	composeConfigFilesLabel     = "com.docker.compose.project.config_files"
	composeEnvironmentFileLabel = "com.docker.compose.project.environment_file"
	composeOneoffLabel          = "com.docker.compose.oneoff"
)

type composeService struct {
	project          string
	service          string
	workingDir       string
	configFiles      []string
	environmentFiles []string
}

func composeServiceFromContainer(info *containerInfo) (*composeService, bool, error) {
	labels := info.Config.Labels
	project := strings.TrimSpace(labels[composeProjectLabel])
	service := strings.TrimSpace(labels[composeServiceLabel])
	if project == "" && service == "" {
		return nil, false, nil
	}
	if project == "" || service == "" {
		return nil, true, fmt.Errorf("container has incomplete Docker Compose metadata")
	}
	if strings.EqualFold(strings.TrimSpace(labels[composeOneoffLabel]), "True") {
		return nil, true, fmt.Errorf("one-off Docker Compose containers cannot be refreshed as a service")
	}

	compose := &composeService{
		project:          project,
		service:          service,
		workingDir:       strings.TrimSpace(labels[composeWorkingDirLabel]),
		configFiles:      splitComposeLabelList(labels[composeConfigFilesLabel]),
		environmentFiles: splitComposeLabelList(labels[composeEnvironmentFileLabel]),
	}
	if compose.workingDir == "" && len(compose.configFiles) == 0 {
		return nil, true, fmt.Errorf("Docker Compose metadata is missing both the project directory and config files")
	}
	return compose, true, nil
}

func splitComposeLabelList(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func (service *composeService) upArgs() []string {
	args := []string{"compose", "--project-name", service.project}
	if service.workingDir != "" {
		args = append(args, "--project-directory", service.workingDir)
	}
	for _, configFile := range service.configFiles {
		args = append(args, "--file", configFile)
	}
	for _, environmentFile := range service.environmentFiles {
		args = append(args, "--env-file", environmentFile)
	}
	return append(args, "up", "--detach", "--no-deps", "--pull", "never", "--force-recreate", service.service)
}
