package node

import (
	"context"
	"encoding/json"

	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// SQLStore stores the last observed node snapshot.
type SQLStore struct{ db *storage.Database }

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

// NewSQLStore creates a node repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// List returns the snapshot in hostname/ID order.
func (s *SQLStore) ReadAll(ctx context.Context) ([]swarm.Node, error) {
	rows, err := s.db.Query(ctx, "SELECT id,hostname,payload FROM nodes ORDER BY hostname,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []swarm.Node{}
	for rows.Next() {
		var (
			id, hostname string
			raw          []byte
			payload      nodePayload
		)
		if err = rows.Scan(&id, &hostname, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		result = append(result, swarm.Node{
			ID:            id,
			Hostname:      hostname,
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
	return result, rows.Err()
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
		if _, err := s.db.Exec(ctx, "DELETE FROM nodes"); err != nil {
			return err
		}
		for _, node := range nodes {
			if _, err := s.ObserveIdentity(ctx, node.ID); err != nil {
				return err
			}
			payload, err := json.Marshal(nodePayload{
				Status:        node.Status,
				Availability:  node.Availability,
				ManagerStatus: node.ManagerStatus,
				EngineVersion: node.EngineVersion,
				Addr:          node.Addr,
				CPUNano:       node.CPUNano,
				MemoryBytes:   node.MemoryBytes,
				Labels:        node.Labels,
			})
			if err != nil {
				return err
			}
			if _, err = s.db.Exec(ctx,
				"INSERT INTO nodes(id,hostname,payload) VALUES (?,?,?)", node.ID, node.Hostname, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
}

// ObserveIdentity remembers IDs across snapshot disappearance and process restarts.
// It returns true only on the first observation, and joins the snapshot transaction.
func (s *SQLStore) ObserveIdentity(ctx context.Context, id string) (bool, error) {
	result, err := s.db.Exec(ctx, "INSERT INTO node_identities VALUES(?) ON CONFLICT DO NOTHING", id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
