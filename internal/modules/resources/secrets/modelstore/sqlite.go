package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

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

// NewSQLStore creates a metadata repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// List returns metadata in stable name/ID order.
func (s *SQLStore) List(ctx context.Context) ([]model.Secret, error) {
	rows, err := s.db.Query(ctx, "SELECT id,name,payload FROM secret_metadata ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Secret{}
	for rows.Next() {
		var (
			id, name string
			payload  []byte
		)
		if err = rows.Scan(&id, &name, &payload); err != nil {
			return nil, err
		}
		secret, buildErr := buildSecret(id, name, payload)
		if buildErr != nil {
			return nil, buildErr
		}
		result = append(result, secret)
	}
	return result, rows.Err()
}

// GetByName returns metadata for one Docker secret.
func (s *SQLStore) GetByName(ctx context.Context, name string) (model.Secret, error) {
	var (
		id      string
		payload []byte
	)
	err := s.db.QueryRow(ctx, "SELECT id,name,payload FROM secret_metadata WHERE name=?", name).
		Scan(&id, &name, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Secret{}, ErrSecretNotFound
	}
	if err != nil {
		return model.Secret{}, err
	}
	return buildSecret(id, name, payload)
}

// Replace atomically replaces the observed snapshot.
func (s *SQLStore) Replace(ctx context.Context, secrets []model.Secret) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := s.db.Exec(ctx, "DELETE FROM secret_metadata"); err != nil {
			return err
		}
		for _, secret := range secrets {
			payload, err := json.Marshal(secretPayload{
				VersionID: secret.VersionID,
				CreatedAt: secret.CreatedAt,
				UpdatedAt: secret.UpdatedAt,
				Driver:    secret.Driver,
				Labels:    secret.Labels,
			})
			if err != nil {
				return err
			}
			if _, err = s.db.Exec(ctx,
				"INSERT INTO secret_metadata(id,name,payload) VALUES (?,?,?)",
				secret.ID, secret.Name, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
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
