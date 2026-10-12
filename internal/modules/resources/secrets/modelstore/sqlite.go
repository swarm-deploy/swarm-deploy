package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

var dialect = goqu.Dialect("sqlite3")

// SQLStore stores Docker secret metadata, never secret contents.
type SQLStore struct{ db *storage.Database }

// secretPayload is the JSON part of a secret_metadata row. It never repeats id, name
// or attributes derived from labels (description, managed, external_path, external_version_id).
type secretPayload struct {
	VersionID uint64            `json:"version_id"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Driver    string            `json:"driver,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// secretRow is the raw columns of a secret_metadata row for sqlx scanning.
type secretRow struct {
	ID      string `db:"id"`
	Name    string `db:"name"`
	Payload []byte `db:"payload"`
}

// NewSQLStore creates a metadata repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// List returns metadata in stable name/ID order.
func (s *SQLStore) List(ctx context.Context) ([]model.Secret, error) {
	query, _, err := dialect.From("secret_metadata").Prepared(true).
		Select("id", "name", "payload").
		Order(goqu.I("name").Asc(), goqu.I("id").Asc()).ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []secretRow
	if err = s.db.Select(ctx, &rows, query); err != nil {
		return nil, err
	}
	result := make([]model.Secret, 0, len(rows))
	for _, row := range rows {
		secret, buildErr := buildSecret(row.ID, row.Name, row.Payload)
		if buildErr != nil {
			return nil, buildErr
		}
		result = append(result, secret)
	}
	return result, nil
}

// GetByName returns metadata for one Docker secret.
func (s *SQLStore) GetByName(ctx context.Context, name string) (model.Secret, error) {
	query, args, err := dialect.From("secret_metadata").Prepared(true).
		Select("id", "name", "payload").Where(goqu.Ex{"name": name}).ToSQL()
	if err != nil {
		return model.Secret{}, err
	}
	var row secretRow
	err = s.db.Get(ctx, &row, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Secret{}, ErrSecretNotFound
	}
	if err != nil {
		return model.Secret{}, err
	}
	return buildSecret(row.ID, row.Name, row.Payload)
}

// Replace atomically replaces the observed snapshot.
func (s *SQLStore) Replace(ctx context.Context, secrets []model.Secret) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		deleteQuery, _, err := dialect.Delete("secret_metadata").Prepared(true).ToSQL()
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, deleteQuery); err != nil {
			return err
		}
		for _, secret := range secrets {
			if err = checkDerivedFields(secret); err != nil {
				return err
			}
			payload, marshalErr := json.Marshal(secretPayload{
				VersionID: secret.VersionID,
				CreatedAt: secret.CreatedAt,
				UpdatedAt: secret.UpdatedAt,
				Driver:    secret.Driver,
				Labels:    secret.Labels,
			})
			if marshalErr != nil {
				return marshalErr
			}
			insertQuery, insertArgs, insertErr := dialect.Insert("secret_metadata").Prepared(true).
				Rows(goqu.Record{"id": secret.ID, "name": secret.Name, "payload": string(payload)}).ToSQL()
			if insertErr != nil {
				return insertErr
			}
			if _, err = s.db.Exec(ctx, insertQuery, insertArgs...); err != nil {
				return err
			}
		}
		return nil
	})
}

// checkDerivedFields rejects secrets whose label-derived fields disagree with Labels,
// because only Labels are persisted and such values would be silently lost.
func checkDerivedFields(secret model.Secret) error {
	derived := map[string][2]string{
		"description":         {secret.Description, secret.Labels[model.DescriptionLabel]},
		"external_path":       {secret.ExternalPath, secret.Labels[model.ExternalPathLabel]},
		"external_version_id": {secret.ExternalVersionID, secret.Labels[model.ExternalVersionIDLabel]},
	}
	for field, values := range derived {
		if values[0] != values[1] {
			return fmt.Errorf("secret %q: %s %q is not backed by labels", secret.Name, field, values[0])
		}
	}
	if secret.Managed != (secret.Labels[model.ManagedLabel] == "true") {
		return fmt.Errorf("secret %q: managed=%t is not backed by labels", secret.Name, secret.Managed)
	}
	return nil
}

func buildSecret(id, name string, raw []byte) (model.Secret, error) {
	var payload secretPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return model.Secret{}, err
	}
	return model.Secret{
		ID:                id,
		Name:              name,
		Description:       payload.Labels[model.DescriptionLabel],
		VersionID:         payload.VersionID,
		CreatedAt:         payload.CreatedAt,
		UpdatedAt:         payload.UpdatedAt,
		Driver:            payload.Driver,
		ExternalPath:      payload.Labels[model.ExternalPathLabel],
		ExternalVersionID: payload.Labels[model.ExternalVersionIDLabel],
		Managed:           payload.Labels[model.ManagedLabel] == "true",
		Labels:            payload.Labels,
	}, nil
}
