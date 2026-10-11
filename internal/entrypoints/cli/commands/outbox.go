package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	go_console "github.com/DrSmithFr/go-console"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// OutboxList lists retained failed deliveries without disclosing payloads.
func OutboxList(script *go_console.Script) go_console.ExitCode { return operateOutbox(script, "list") }

// OutboxReplay reschedules one explicitly identified failed destination.
func OutboxReplay(script *go_console.Script) go_console.ExitCode {
	return operateOutbox(script, "replay")
}

// OutboxDiscard waives one explicitly identified failed delivery.
func OutboxDiscard(script *go_console.Script) go_console.ExitCode {
	return operateOutbox(script, "discard")
}

func operateOutbox(script *go_console.Script, action string) go_console.ExitCode {
	ctx := context.Background()
	dataDir := script.Input.Argument("dataDir")
	// An operator typo must not initialize a new empty database.
	info, err := os.Stat(filepath.Join(dataDir, "swarm-deploy.sqlite"))
	if err != nil || !info.Mode().IsRegular() {
		script.PrintError("existing swarm-deploy.sqlite is required")
		return go_console.ExitError
	}
	db, err := storage.Open(ctx, dataDir)
	if err != nil {
		script.PrintError(fmt.Sprintf("open database: %v", err))
		return go_console.ExitError
	}
	defer db.Close()
	bus := outbox.New(db)
	switch action {
	case "list":
		var failed []outbox.FailedDelivery
		failed, err = bus.ListFailed(ctx)
		if err == nil {
			var encoded []byte
			encoded, err = json.MarshalIndent(failed, "", "  ")
			if err == nil {
				script.PrintText(string(encoded))
			}
		}
	case "replay":
		err = bus.Replay(ctx, script.Input.Argument("eventID"), script.Input.Argument("subscriptionID"))
	case "discard":
		err = bus.Discard(ctx, script.Input.Argument("eventID"), script.Input.Argument("subscriptionID"))
	}
	if err != nil {
		script.PrintError(fmt.Sprintf("outbox %s: %v", action, err))
		return go_console.ExitError
	}
	return go_console.ExitSuccess
}
