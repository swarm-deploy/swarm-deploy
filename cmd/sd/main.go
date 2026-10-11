package main

import (
	go_console "github.com/DrSmithFr/go-console"
	"github.com/DrSmithFr/go-console/input/argument"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/cli/commands"
)

var (
	Version   = "0.1.0"
	BuildDate = "2026-05-26 23:51:00"
)

func main() {
	cmd := go_console.Command{
		Description: "Swarm Deploy CLI",
		BuildInfo: &go_console.BuildInfo{
			Name:      "swarm-deploy-cli",
			Version:   Version,
			BuildFlag: BuildDate,
		},
		Scripts: []*go_console.Script{
			{Name: "outbox-list", Description: "List failed outbox deliveries (no payloads)",
				Arguments: []go_console.Argument{{Name: "dataDir", Value: argument.Required}},
				Runner:    commands.OutboxList},
			{Name: "outbox-replay", Description: "Replay one failed delivery",
				Arguments: []go_console.Argument{{Name: "dataDir", Value: argument.Required},
					{Name: "eventID", Value: argument.Required},
					{Name: "subscriptionID", Value: argument.Required},
				},
				Runner: commands.OutboxReplay},
			{Name: "outbox-discard", Description: "Permanently waive one failed delivery",
				Arguments: []go_console.Argument{{Name: "dataDir", Value: argument.Required},
					{Name: "eventID", Value: argument.Required},
					{Name: "subscriptionID", Value: argument.Required},
				},
				Runner: commands.OutboxDiscard},
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
				Runner: commands.Lint,
			},
		},
	}

	cmd.Run()
}
