package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore stores Docker secret metadata, never secret contents.
type SQLStore struct{ db storage.Database }

// secretPayload is the JSON part of a secret_metadata row. It never repeats id, name
// or attributes derived from labels (description, managed, external_path, external_version_id).
type secretPayload struct {
	VersionID uint64            `json:"version_id"`
	Driver    string            `json:"driver,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// secretRow is the raw columns of a secret_metadata row for sqlx scanning.
type secretRow struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
	Payload   []byte    `db:"payload"`
}

// NewSQLStore creates a metadata repository.
func NewSQLStore(db storage.Database) *SQLStore { return &SQLStore{db: db} }

// List returns metadata in stable name/ID order.
func (s *SQLStore) List(ctx context.Context) ([]model.Secret, error) {
	query, _, err := s.db.Dialect().From("secret_metadata").Prepared(true).
		Select("id", "name", "created_at", "updated_at", "payload").
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
		secret, buildErr := buildSecret(row)
		if buildErr != nil {
			return nil, buildErr
		}
		result = append(result, secret)
	}
	return result, nil
}

// GetByID returns metadata for one Docker secret by ID.
func (s *SQLStore) GetByID(ctx context.Context, id string) (model.Secret, error) {
	return s.getBy(ctx, goqu.Ex{"id": id})
}

// GetByName returns metadata for one Docker secret by name.
func (s *SQLStore) GetByName(ctx context.Context, name string) (model.Secret, error) {
	return s.getBy(ctx, goqu.Ex{"name": name})
}

func (s *SQLStore) getBy(ctx context.Context, where goqu.Ex) (model.Secret, error) {
	query, args, err := s.db.Dialect().From("secret_metadata").Prepared(true).
		Select("id", "name", "created_at", "updated_at", "payload").Where(where).ToSQL()
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
	return buildSecret(row)
}

// Replace atomically replaces the observed snapshot.
func (s *SQLStore) Replace(ctx context.Context, secrets []model.Secret) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		deleteQuery, _, err := s.db.Dialect().Delete("secret_metadata").Prepared(true).ToSQL()
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, deleteQuery); err != nil {
			return err
		}
		for _, secret := range secrets {
			payload, payloadErr := secretPayloadForStorage(secret)
			if payloadErr != nil {
				return payloadErr
			}
			insertQuery, insertArgs, insertErr := s.db.Dialect().Insert("secret_metadata").Prepared(true).
				Rows(goqu.Record{
					"id": secret.ID, "name": secret.Name, "created_at": secret.CreatedAt.UTC(),
					"updated_at": secret.UpdatedAt.UTC(), "payload": string(payload),
				}).ToSQL()
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

func secretPayloadForStorage(secret model.Secret) ([]byte, error) {
	if secret.CreatedAt.IsZero() || secret.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("secret %q: timestamps are required", secret.Name)
	}
	if err := checkDerivedFields(secret); err != nil {
		return nil, err
	}
	return json.Marshal(secretPayload{
		VersionID: secret.VersionID,
		Driver:    secret.Driver,
		Labels:    secret.Labels,
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

func buildSecret(row secretRow) (model.Secret, error) {
	var payload secretPayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return model.Secret{}, err
	}
	return model.Secret{
		ID:                row.ID,
		Name:              row.Name,
		Description:       payload.Labels[model.DescriptionLabel],
		VersionID:         payload.VersionID,
		CreatedAt:         row.CreatedAt.UTC(),
		UpdatedAt:         row.UpdatedAt.UTC(),
		Driver:            payload.Driver,
		ExternalPath:      payload.Labels[model.ExternalPathLabel],
		ExternalVersionID: payload.Labels[model.ExternalVersionIDLabel],
		Managed:           payload.Labels[model.ManagedLabel] == "true",
		Labels:            payload.Labels,
	}, nil
}
