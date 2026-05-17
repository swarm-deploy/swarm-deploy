package main

import (
	"fmt"
	"os"

	go_console "github.com/DrSmithFr/go-console"
	"github.com/DrSmithFr/go-console/input/argument"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

const defaultConfigPath = "./swarm-deploy.yaml"

func main() {
	cmd := go_console.Command{
		Description: "Swarm Deploy CLI",
		Scripts: []*go_console.Script{
			{
				Name:        "lint",
				Description: "Validate swarm-deploy YAML config",
				InputDefinition: []go_console.InputDefinitionInterface{
					argument.New("configPath", argument.NotRequired),
				},
				Runner: lintRunner,
			},
		},
	}

	os.Exit(cmd.Execute())
}

func lintRunner(script *go_console.Script) go_console.ExitCode {
	configPath := script.Input.GetStringArgument("configPath", defaultConfigPath)

	_, err := config.Load(configPath)
	if err != nil {
		script.PrintError(fmt.Sprintf("Config is invalid: %v", err))

		return go_console.Error
	}

	script.PrintText(fmt.Sprintf("Config %q is valid", configPath))

	return go_console.Success
}
