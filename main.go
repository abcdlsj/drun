package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// Color constants for terminal output
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"
	ColorBold   = "\033[1m"
)

// Helper functions for colored output
func printInfo(format string, args ...interface{}) {
	fmt.Printf(ColorBlue+"[INFO]"+ColorReset+" "+format, args...)
}

func printSuccess(format string, args ...interface{}) {
	fmt.Printf(ColorGreen+"[SUCCESS]"+ColorReset+" "+format, args...)
}

func printWarning(format string, args ...interface{}) {
	fmt.Printf(ColorYellow+"[WARNING]"+ColorReset+" "+format, args...)
}

func printError(format string, args ...interface{}) {
	fmt.Printf(ColorRed+"[ERROR]"+ColorReset+" "+format, args...)
}

func printCommand(command string) {
	fmt.Printf(ColorCyan+"Generated command:"+ColorReset+"\n")
	fmt.Printf(ColorBold+"%s"+ColorReset+"\n\n", command)
}

func printPrompt(prompt string) {
	fmt.Printf(ColorYellow+prompt+ColorReset)
}

type ContainerInfo struct {
	Config struct {
		Image string   `json:"Image"`
		Cmd   []string `json:"Cmd"`
		Env   []string `json:"Env"`
	} `json:"Config"`
	HostConfig struct {
		Binds           []string          `json:"Binds"`
		PortBindings    map[string][]Port `json:"PortBindings"`
		RestartPolicy   RestartPolicy     `json:"RestartPolicy"`
		NetworkMode     string            `json:"NetworkMode"`
		Privileged      bool              `json:"Privileged"`
		PublishAllPorts bool              `json:"PublishAllPorts"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]NetworkInfo `json:"Networks"`
	} `json:"NetworkSettings"`
	Name string `json:"Name"`
}

type Port struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type RestartPolicy struct {
	Name              string `json:"Name"`
	MaximumRetryCount int    `json:"MaximumRetryCount"`
}

type NetworkInfo struct {
	NetworkID string `json:"NetworkID"`
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: drun <container_name>")
	}

	containerName := os.Args[1]
	
	printInfo("Processing container: %s\n", containerName)
	
	containerInfo, err := getContainerInfo(containerName)
	if err != nil {
		printError("Failed to get container info: %v\n", err)
		os.Exit(1)
	}

	imageName := containerInfo.Config.Image
	printInfo("Container image: %s\n", imageName)

	if err := stopAndRemoveContainer(containerName); err != nil {
		printError("Failed to stop/remove container: %v\n", err)
		os.Exit(1)
	}

	if err := pullLatestImage(imageName); err != nil {
		printError("Failed to pull latest image: %v\n", err)
		os.Exit(1)
	}

	runCommand := generateRunCommand(containerInfo)
	printCommand(runCommand)
	
	if !confirmExecution() {
		printWarning("Operation cancelled by user.\n")
		return
	}
	
	if err := executeCommand(runCommand); err != nil {
		printError("Failed to run container: %v\n", err)
		os.Exit(1)
	}

	printSuccess("Container %s has been successfully restarted with latest image\n", containerName)
}

func getContainerInfo(containerName string) (*ContainerInfo, error) {
	cmd := exec.Command("docker", "inspect", containerName)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %v", err)
	}

	var containers []ContainerInfo
	if err := json.Unmarshal(output, &containers); err != nil {
		return nil, fmt.Errorf("failed to parse container info: %v", err)
	}

	if len(containers) == 0 {
		return nil, fmt.Errorf("container not found")
	}

	return &containers[0], nil
}

func stopAndRemoveContainer(containerName string) error {
	printInfo("Stopping container %s...\n", containerName)
	if err := exec.Command("docker", "stop", containerName).Run(); err != nil {
		return fmt.Errorf("failed to stop container: %v", err)
	}

	printInfo("Removing container %s...\n", containerName)
	if err := exec.Command("docker", "rm", containerName).Run(); err != nil {
		return fmt.Errorf("failed to remove container: %v", err)
	}

	return nil
}

func pullLatestImage(imageName string) error {
	printInfo("Pulling latest image %s...\n", imageName)
	cmd := exec.Command("docker", "pull", imageName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pull image: %v", err)
	}
	return nil
}

func generateRunCommand(info *ContainerInfo) string {
	var parts []string
	parts = append(parts, "docker", "run", "-d")

	containerName := strings.TrimPrefix(info.Name, "/")
	parts = append(parts, "--name", containerName)

	if info.HostConfig.RestartPolicy.Name != "" {
		parts = append(parts, "--restart", info.HostConfig.RestartPolicy.Name)
	}

	for _, bind := range info.HostConfig.Binds {
		parts = append(parts, "-v", bind)
	}

	for port, bindings := range info.HostConfig.PortBindings {
		for _, binding := range bindings {
			if binding.HostPort != "" {
				hostPort := binding.HostPort
				parts = append(parts, "-p", fmt.Sprintf("%s:%s", hostPort, port))
			}
		}
	}

	for _, env := range info.Config.Env {
		if !shouldSkipEnv(env) {
			parts = append(parts, "-e", env)
		}
	}

	if info.HostConfig.Privileged {
		parts = append(parts, "--privileged")
	}

	if info.HostConfig.PublishAllPorts {
		parts = append(parts, "-P")
	}

	if info.HostConfig.NetworkMode != "" && info.HostConfig.NetworkMode != "default" {
		parts = append(parts, "--network", info.HostConfig.NetworkMode)
	}

	parts = append(parts, info.Config.Image)

	if len(info.Config.Cmd) > 0 {
		parts = append(parts, info.Config.Cmd...)
	}

	return strings.Join(parts, " ")
}

func shouldSkipEnv(env string) bool {
	skipPatterns := []string{
		"PATH=",
		"HOSTNAME=",
		"HOME=",
		"TERM=",
	}

	for _, pattern := range skipPatterns {
		if strings.HasPrefix(env, pattern) {
			return true
		}
	}

	return false
}

func confirmExecution() bool {
	reader := bufio.NewReader(os.Stdin)
	printPrompt("Do you want to execute this command? (y/N): ")
	
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

func executeCommand(command string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}