// Package legacyimport performs the one-time, all-or-nothing JSON migration.
package legacyimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	gitmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	gitstore "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	recommendationmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	recommendationstore "github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/node"
	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	servicemodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	servicestore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const importID = "legacy-json-v1"

type snapshot struct {
	runtime         gitmodel.Runtime
	history         []history.Entry
	alerts          []alertmodel.Alert
	recommendations []recommendationmodel.Recommendation
	nodes           []swarm.Node
	services        []servicemodel.Info
	secrets         []secretmodel.Secret
	chats           []conversation.Chat
}

// Run validates all legacy inputs before opening one import transaction. Originals
// remain untouched. Modules and workers must start only after Run succeeds.
func Run(ctx context.Context, db *storage.Database, dataDir string) error {
	done, err := completed(ctx, db)
	if err != nil || done {
		return err
	}
	data, err := preflight(dataDir)
	if err != nil {
		return fmt.Errorf("legacy preflight: %w", err)
	}
	return db.WithinTransaction(ctx, func(ctx context.Context) error {
		already, checkErr := completed(ctx, db)
		if checkErr != nil || already {
			return checkErr
		}
		if checkErr = requireEmpty(ctx, db); checkErr != nil {
			return checkErr
		}
		if checkErr = data.save(ctx, db); checkErr != nil {
			return fmt.Errorf("import legacy data: %w", checkErr)
		}
		_, writeErr := db.Get(ctx).ExecContext(ctx,
			"INSERT INTO legacy_imports VALUES (?,?)", importID, time.Now().UnixMilli())
		return writeErr
	})
}

func completed(ctx context.Context, db *storage.Database) (bool, error) {
	var count int
	err := db.Get(ctx).QueryRowContext(ctx, "SELECT count(*) FROM legacy_imports WHERE id=?", importID).Scan(&count)
	return count == 1, err
}

func requireEmpty(ctx context.Context, db *storage.Database) error {
	for _, table := range []string{"gitops_runtime", "event_history", "alerts", "recommendations", "nodes", "services",
		"secret_metadata", "assistant_chats", "assistant_turns", "outbox_events", "outbox_deliveries", "alert_events",
		"deployments", "desired_snapshots", "service_catalog_receipts", "projection_versions", "node_identities"} {
		var count int
		if err := db.Get(ctx).QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("unmarked database already contains %s; refusing to overwrite", table)
		}
	}
	return nil
}

func preflight(dir string) (snapshot, error) {
	var data snapshot
	if err := readJSON(dir, "controller.state.json", &data.runtime); err != nil {
		return data, err
	}
	if err := readJSON(dir, "event-history.json", &data.history); err != nil {
		return data, err
	}
	if err := readJSON(dir, "nodes.json", &data.nodes); err != nil {
		return data, err
	}
	var alerts struct {
		// Alerts preserves the legacy wrapper.
		Alerts []alertmodel.Alert `json:"alerts"`
	}
	if err := readJSON(dir, "alerts.state.json", &alerts); err != nil {
		return data, err
	}
	data.alerts = alerts.Alerts
	var secrets struct {
		// Secrets contains metadata only.
		Secrets []secretmodel.Secret `json:"secrets"`
	}
	if err := readJSON(dir, "secrets.state.json", &secrets); err != nil {
		return data, err
	}
	data.secrets = secrets.Secrets
	var err error
	data.recommendations, err = readRecommendations(dir)
	if err != nil {
		return data, err
	}
	payload, err := os.ReadFile(filepath.Join(dir, "services.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return data, fmt.Errorf("read services.json: %w", err)
	}
	if err == nil {
		data.services, err = servicestore.DecodeLegacy(payload)
		if err != nil {
			return data, fmt.Errorf("decode services.json: %w", err)
		}
	}
	data.chats, err = readChats(filepath.Join(dir, "assistant", "chats"))
	if err != nil {
		return data, err
	}
	return data, data.validate()
}

func readJSON(dir, name string, target any) error {
	payload, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if err = validateJSONShape(name, payload); err != nil {
		return err
	}
	if err = json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	return nil
}

func (s snapshot) save(ctx context.Context, db *storage.Database) error {
	if err := gitstore.NewSQLStore(db).Save(ctx, s.runtime); err != nil {
		return err
	}
	historyStore, err := history.NewSQLStore(db, max(len(s.history), 1))
	if err != nil {
		return err
	}
	for i := range s.history {
		if s.history[i].ID == "" {
			s.history[i].ID = uuid.NewString()
		}
	}
	if err = historyStore.Import(ctx, s.history); err != nil {
		return err
	}
	if err = alertstore.NewSQLStore(db).Import(ctx, s.alerts); err != nil {
		return err
	}
	if err = recommendationstore.NewSQLStore(db).Import(ctx, s.recommendations); err != nil {
		return err
	}
	if err = node.NewSQLStore(db).ReplaceSnapshot(ctx, s.nodes); err != nil {
		return err
	}
	if err = secretstore.NewSQLStore(db).Replace(ctx, s.secrets); err != nil {
		return err
	}
	stacks := map[string][]servicemodel.Info{}
	for _, service := range s.services {
		stacks[service.Stack] = append(stacks[service.Stack], service)
	}
	for stack, services := range stacks {
		if err = servicestore.NewSQLStore(db).ReplaceStack(ctx, stack, services); err != nil {
			return err
		}
	}
	for _, chat := range s.chats {
		if err = conversation.NewSQLHistoryStorage(db).Save(ctx, chat); err != nil {
			return err
		}
	}
	return nil
}

func readRecommendations(dir string) ([]recommendationmodel.Recommendation, error) {
	var recommendations struct {
		// List is authoritative; legacy lookup indexes are validated separately.
		List []recommendationmodel.Recommendation `json:"list"`
		// Stacks maps stack identities to list positions.
		Stacks map[string][]int `json:"stacks"`
		// IDs maps recommendation identities to list positions.
		IDs map[string][]int `json:"ids"`
	}
	if err := readJSON(dir, "recommendations.state.json", &recommendations); err != nil {
		return nil, err
	}
	for stack, indexes := range recommendations.Stacks {
		for _, i := range indexes {
			if i < 0 || i >= len(recommendations.List) || recommendations.List[i].Subject.Stack != stack {
				return nil, fmt.Errorf("invalid recommendation stack index")
			}
		}
	}
	for id, indexes := range recommendations.IDs {
		for _, i := range indexes {
			if i < 0 || i >= len(recommendations.List) || recommendations.List[i].ID() != id {
				return nil, fmt.Errorf("invalid recommendation ID index")
			}
		}
	}
	return recommendations.List, nil
}
