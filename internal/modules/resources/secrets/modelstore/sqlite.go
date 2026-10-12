package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore stores Docker secret metadata, never secret contents.
type SQLStore struct{ db *storage.Database }

// NewSQLStore creates a metadata repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// List returns metadata in stable name/ID order.
func (s *SQLStore) List(ctx context.Context) ([]model.Secret, error) {
	rows, err := s.db.Query(ctx, "SELECT payload FROM secret_metadata ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Secret{}
	for rows.Next() {
		var payload []byte
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		var secret model.Secret
		if err = json.Unmarshal(payload, &secret); err != nil {
			return nil, err
		}
		result = append(result, secret)
	}
	return result, rows.Err()
}

// GetByName returns metadata for one Docker secret.
func (s *SQLStore) GetByName(ctx context.Context, name string) (model.Secret, error) {
	var payload []byte
	err := s.db.QueryRow(ctx, "SELECT payload FROM secret_metadata WHERE name=?", name).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Secret{}, ErrSecretNotFound
	}
	if err != nil {
		return model.Secret{}, err
	}
	var secret model.Secret
	err = json.Unmarshal(payload, &secret)
	return secret, err
}

// Replace atomically replaces the observed snapshot.
func (s *SQLStore) Replace(ctx context.Context, secrets []model.Secret) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := s.db.Exec(ctx, "DELETE FROM secret_metadata"); err != nil {
			return err
		}
		for _, secret := range secrets {
			payload, err := json.Marshal(secret)
			if err != nil {
				return err
			}
			if _, err = s.db.Exec(ctx,
				"INSERT INTO secret_metadata VALUES (?,?,?)", secret.ID, secret.Name, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
}
