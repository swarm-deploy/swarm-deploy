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
	return storage.QueryJSON[model.Secret](ctx, s.db.Get, "SELECT payload FROM secret_metadata ORDER BY name,id")
}

// GetByName returns metadata for one Docker secret.
func (s *SQLStore) GetByName(ctx context.Context, name string) (model.Secret, error) {
	secret, err := storage.GetJSON[model.Secret](ctx, s.db.Get, "SELECT payload FROM secret_metadata WHERE name=?", name)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Secret{}, ErrSecretNotFound
	}
	return secret, err
}

// Replace atomically replaces the observed snapshot.
func (s *SQLStore) Replace(ctx context.Context, secrets []model.Secret) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := s.db.Get(ctx).ExecContext(ctx, "DELETE FROM secret_metadata"); err != nil {
			return err
		}
		for _, secret := range secrets {
			payload, err := json.Marshal(secret)
			if err != nil {
				return err
			}
			if _, err = s.db.Get(ctx).ExecContext(ctx,
				"INSERT INTO secret_metadata VALUES (?,?,?)", secret.ID, secret.Name, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
}
