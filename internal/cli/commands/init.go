package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	go_console "github.com/DrSmithFr/go-console"
	"github.com/artarts36/specw"
	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/labelsdict"
)

type InitCommand struct {
}

func Init(script *go_console.Script) go_console.ExitCode {
	cmd := &InitCommand{}

	return cmd.Run(script)
}

func (c *InitCommand) Run(script *go_console.Script) go_console.ExitCode {
	_ = &config.Config{
		Spec: config.Spec{
			Git: config.GitSpec{
				Repository: "<enter your repository url>",
				Branch:     "master",
				Auth: config.GitAuthSpec{
					HTTP: config.GitHTTPAuth{
						Token: specw.File{
							Path: "/var/run/secrets/sd-git-token",
						},
					},
				},
			},
			Sync: config.SyncSpec{
				Mode: config.SyncModePull,
			},
		},
	}

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		slog.Error("failed to init docker client", slog.Any("err", err))
	}

	stacks, err := c.collectStacks(dockerClient)
	if err != nil {
		slog.Error("failed to collect stacks", slog.Any("err", err))
	}

	for _, file := range stacks {
		content, err := file.MarshalYAML()
		if err != nil {
			panic(err)
		}

		err = os.WriteFile(file.Path, content, 0775)
		if err != nil {
			panic(err)
		}
	}

	return go_console.ExitSuccess
}

func (c *InitCommand) collectStacks(dockerClient *client.Client) (map[string]compose.File, error) {
	containers, err := dockerClient.ContainerList(context.Background(), container.ListOptions{})
	if err != nil {

	}

	return c.collectStacksFromContainers(containers)
}

func (c *InitCommand) collectStacksFromContainers(containers []container.Summary) (map[string]compose.File, error) {
	files := map[string]compose.File{}

	for _, cont := range containers {
		stackName := cont.Labels[labelsdict.ComposeContainerProject]
		if stackName == "" {
			stackName = "other"
		}

		file, fileExists := files[stackName]
		if !fileExists {
			file = compose.File{
				Path: fmt.Sprintf("./%s", stackName),
				Compose: compose.Compose{
					Services: make(compose.Services, 0),
				},
			}
		}

		service := compose.Service{
			Name:  cont.Labels[labelsdict.ComposeContainerService],
			Image: cont.Image,
			Command: compose.Command{
				Args: []string{
					cont.Command,
				},
			},
			Ports: compose.ServicePorts{
				Ports: make([]compose.ServicePort, 0, len(cont.Ports)),
			},
		}

		for _, port := range cont.Ports {
			service.Ports.Ports = append(service.Ports.Ports, compose.ServicePort{
				Target:    int(port.PrivatePort),
				Published: int(port.PublicPort),
				Protocol:  dockerswarm.PortConfigProtocol(port.Type),
			})
		}

		if service.Name == "" {
			service.Name = cont.Names[0]
		}

		file.Compose.Services = append(file.Compose.Services, service)
		files[stackName] = file
	}

	return files, nil
}
