package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const storeFileModePrivate = 0o600

// Store persists nodes snapshot in a JSON file.
type Store struct {
	mu      sync.RWMutex
	path    string
	rows    []swarm.Node
	nodeMap map[string]swarm.Node
}

// NewNodeStore creates nodes store and loads saved rows from disk.
func NewNodeStore(path string) (*Store, error) {
	s := &Store{
		path: path,
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

// Map returns map<node.id>swarm.Node.
func (s *Store) Map() map[string]swarm.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.nodeMap
}

// List returns a copy of all saved nodes.
func (s *Store) List() []swarm.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return cloneNodes(s.rows)
}

// Replace replaces nodes snapshot and saves it to disk.
func (s *Store) Replace(nodes []swarm.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.setNodes(nodes)

	return s.flushLocked()
}

func (s *Store) load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create nodes dir: %w", err)
	}

	payload, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("read nodes file: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	var rows []swarm.Node
	if unmarshalErr := json.Unmarshal(payload, &rows); unmarshalErr != nil {
		return fmt.Errorf("decode nodes file: %w", unmarshalErr)
	}

	s.setNodes(rows)
	return nil
}

func (s *Store) flushLocked() error {
	payload, err := json.Marshal(s.rows)
	if err != nil {
		return fmt.Errorf("encode nodes file: %w", err)
	}

	tmpPath := fmt.Sprintf("%s.tmp", s.path)
	if writeErr := os.WriteFile(tmpPath, payload, storeFileModePrivate); writeErr != nil {
		return fmt.Errorf("write nodes temp file: %w", writeErr)
	}
	if renameErr := os.Rename(tmpPath, s.path); renameErr != nil {
		return fmt.Errorf("replace nodes file: %w", renameErr)
	}

	return nil
}

func (s *Store) setNodes(rows []swarm.Node) {
	s.nodeMap = make(map[string]swarm.Node, len(s.rows))
	s.rows = make([]swarm.Node, len(rows))

	for i, row := range rows {
		node := cloneNode(row)

		s.rows[i] = node
		s.nodeMap[row.ID] = node
	}

	sortNodes(s.rows)
}

func sortNodes(nodes []swarm.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Hostname != nodes[j].Hostname {
			return nodes[i].Hostname < nodes[j].Hostname
		}

		return nodes[i].ID < nodes[j].ID
	})
}

func cloneNodes(nodes []swarm.Node) []swarm.Node {
	if len(nodes) == 0 {
		return nil
	}

	out := make([]swarm.Node, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, cloneNode(node))
	}

	return out
}

func cloneNode(node swarm.Node) swarm.Node {
	if len(node.Labels) == 0 {
		return node
	}

	node.Labels = cloneStringMap(node.Labels)
	return node
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}

	return out
}
