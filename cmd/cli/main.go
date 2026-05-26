package main

import (
	"fmt"
	"path/filepath"
	"strings"

	go_console "github.com/DrSmithFr/go-console"
	"github.com/DrSmithFr/go-console/input/argument"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func main() {
	cmd := go_console.Command{
		Description: "Swarm Deploy CLI",
		Scripts: []*go_console.Script{
			{
				Name:        "lint",
				Description: "Validate swarm-deploy YAML config",
				Arguments: []go_console.Argument{
					{
						Name:         "configPath",
						Value:        argument.Optional,
						DefaultValue: "./swarm-deploy.yaml",
					},
				},
				Runner: lintRunner,
			},
		},
	}

	cmd.Run()
}

const listExpandedLen = 5

func lintRunner(script *go_console.Script) go_console.ExitCode {
	configPath := script.Input.Argument("configPath")

	cfg, err := config.Unmarshal(configPath)
	if err != nil {
		script.PrintError(fmt.Sprintf("Config is invalid: %v", err))

		return go_console.ExitError
	}

	stackNames := make([]string, len(cfg.Spec.Stacks))
	networkNames := make([]string, len(cfg.Spec.Networks))
	composeFiles := make([]string, len(cfg.Spec.Stacks))
	serviceNames := make([]string, 0)
	baseDir := filepath.Dir(configPath)

	for i, stack := range cfg.Spec.Stacks {
		stackNames[i] = stack.Name
		composeFiles[i] = fmt.Sprintf("%s/%s", baseDir, stack.ComposeFile)
	}

	for i, network := range cfg.Spec.Networks {
		networkNames[i] = network.Name
	}

	composeLoader := compose.NewFileLoader()

	for _, composeFile := range composeFiles {
		file, cerr := composeLoader.Load(composeFile)
		if cerr != nil {
			script.PrintError(fmt.Sprintf("Compose file %s is invalid: %v", composeFile, cerr))
			return go_console.ExitError
		}

		for _, service := range file.Compose.Services {
			serviceNames = append(serviceNames, service.Name)
		}
	}

	script.PrintText(fmt.Sprintf("Config %q is valid", configPath))
	script.PrintNewLine(1)

	script.PrintText(fmt.Sprintf(
		"Found %d stacks, %d networks, %d services",
		len(cfg.Spec.Stacks),
		len(cfg.Spec.Networks),
		len(serviceNames),
	))
	script.PrintNewLine(1)

	if len(stackNames) > 0 {
		if len(stackNames) > listExpandedLen {
			script.PrintText("Stacks")
			script.PrintListing(stackNames)
		} else {
			script.PrintText("Stacks: " + strings.Join(stackNames, ", "))
			script.PrintNewLine(1)
		}
	}

	if len(networkNames) > 0 {
		if len(stackNames) > listExpandedLen {
			script.PrintText("Networks")
			script.PrintListing(networkNames)
		} else {
			script.PrintText("Networks: " + strings.Join(networkNames, ", "))
			script.PrintNewLine(1)
		}
	}

	if len(serviceNames) > 0 {
		if len(stackNames) > listExpandedLen {
			script.PrintText("Services")
			script.PrintListing(serviceNames)
		} else {
			script.PrintText("Services: " + strings.Join(serviceNames, ", "))
			script.PrintNewLine(1)
		}
	}

	return go_console.ExitSuccess
}
