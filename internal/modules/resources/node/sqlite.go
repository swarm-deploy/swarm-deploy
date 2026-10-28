package node

import (
	"context"
	"encoding/json"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// SQLStore stores the last observed node snapshot.
type SQLStore struct{ db storage.Database }

// Repository provides context-aware node snapshot operations.
type Repository interface {
	// ReadAll lists observed nodes.
	ReadAll(context.Context) ([]swarm.Node, error)
	// Lookup returns the node map.
	Lookup(context.Context) (map[string]swarm.Node, error)
	// ReplaceSnapshot persists the complete observed snapshot.
	ReplaceSnapshot(context.Context, []swarm.Node) error
}

// nodePayload is the JSON part of a nodes row. It never repeats id or hostname.
type nodePayload struct {
	Status        string                  `json:"status"`
	Availability  string                  `json:"availability"`
	ManagerStatus swarm.NodeManagerStatus `json:"manager_status"`
	EngineVersion string                  `json:"engine_version"`
	Addr          string                  `json:"addr"`
	CPUNano       int64                   `json:"cpu_nano"`
	MemoryBytes   int64                   `json:"memory_bytes"`
	Labels        map[string]string       `json:"labels"`
}

// nodeRow is the raw columns of a nodes row for sqlx scanning.
type nodeRow struct {
	ID       string `db:"id"`
	Hostname string `db:"hostname"`
	Payload  []byte `db:"payload"`
}

// NewSQLStore creates a node repository.
func NewSQLStore(db storage.Database) *SQLStore { return &SQLStore{db: db} }

// List returns the snapshot in hostname/ID order.
func (s *SQLStore) ReadAll(ctx context.Context) ([]swarm.Node, error) {
	query, _, err := s.db.Dialect().From("nodes").Prepared(true).
		Select("id", "hostname", "payload").
		Order(goqu.I("hostname").Asc(), goqu.I("id").Asc()).ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []nodeRow
	if err = s.db.Select(ctx, &rows, query); err != nil {
		return nil, err
	}
	result := make([]swarm.Node, 0, len(rows))
	for _, row := range rows {
		var payload nodePayload
		if err = json.Unmarshal(row.Payload, &payload); err != nil {
			return nil, err
		}
		result = append(result, swarm.Node{
			ID:            row.ID,
			Hostname:      row.Hostname,
			Status:        payload.Status,
			Availability:  payload.Availability,
			ManagerStatus: payload.ManagerStatus,
			EngineVersion: payload.EngineVersion,
			Addr:          payload.Addr,
			CPUNano:       payload.CPUNano,
			MemoryBytes:   payload.MemoryBytes,
			Labels:        payload.Labels,
		})
	}
	return result, nil
}

// Map returns an independent node lookup.
func (s *SQLStore) Lookup(ctx context.Context) (map[string]swarm.Node, error) {
	nodes, err := s.ReadAll(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]swarm.Node, len(nodes))
	for _, node := range nodes {
		result[node.ID] = node
	}
	return result, nil
}

// Replace atomically replaces the observed snapshot.
func (s *SQLStore) ReplaceSnapshot(ctx context.Context, nodes []swarm.Node) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		deleteQuery, _, err := s.db.Dialect().Delete("nodes").Prepared(true).ToSQL()
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, deleteQuery); err != nil {
			return err
		}
		for _, node := range nodes {
			if _, err = s.ObserveIdentity(ctx, node.ID); err != nil {
				return err
			}
			payload, marshalErr := json.Marshal(nodePayload{
				Status:        node.Status,
				Availability:  node.Availability,
				ManagerStatus: node.ManagerStatus,
				EngineVersion: node.EngineVersion,
				Addr:          node.Addr,
				CPUNano:       node.CPUNano,
				MemoryBytes:   node.MemoryBytes,
				Labels:        node.Labels,
			})
			if marshalErr != nil {
				return marshalErr
			}
			insertQuery, insertArgs, insertErr := s.db.Dialect().Insert("nodes").Prepared(true).
				Rows(goqu.Record{"id": node.ID, "hostname": node.Hostname, "payload": string(payload)}).ToSQL()
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

// ObserveIdentity remembers IDs across snapshot disappearance and process restarts.
// It returns true only on the first observation, and joins the snapshot transaction.
func (s *SQLStore) ObserveIdentity(ctx context.Context, id string) (bool, error) {
	query, args, err := s.db.Dialect().Insert("node_identities").Prepared(true).
		Rows(goqu.Record{"id": id}).OnConflict(goqu.DoNothing()).ToSQL()
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
