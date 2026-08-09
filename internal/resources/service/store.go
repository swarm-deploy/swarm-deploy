package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/resources/service/metadata"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/knownapp"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/webroute"
)

const fileModePrivate = 0o600

// Store persists service metadata in a JSON file.
type Store struct {
	mu             sync.RWMutex
	path           string
	rows           []Info
	byServiceNames map[string]int
}

// NewStore creates service store and loads saved rows from disk.
func NewStore(path string) (*Store, error) {
	s := &Store{
		path: path,
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

// List returns a copy of all saved services.
func (s *Store) List() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Info, len(s.rows))
	copy(out, s.rows)
	return out
}

// Get returns saved service metadata by stack and service names.
func (s *Store) Get(stackName string, serviceName string) (Info, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rowIndex, ok := s.byServiceNames[serviceKey(stackName, serviceName)]
	if !ok {
		return Info{}, false
	}

	return s.rows[rowIndex], true
}

// ReplaceStack replaces stack services with a new snapshot and saves it to disk.
func (s *Store) ReplaceStack(stackName string, services []Info) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	updated := make([]Info, 0, len(s.rows)+len(services))
	for _, current := range s.rows {
		if current.Stack == stackName {
			continue
		}
		updated = append(updated, current)
	}
	for _, service := range services {
		if service.Name == "" {
			continue
		}
		service.Stack = stackName
		updated = append(updated, service)
	}

	sortInfos(updated)
	s.rows = updated
	s.reindexLocked()

	return s.flushLocked()
}

func (s *Store) load() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create services dir: %w", err)
	}

	payload, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("read services file: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}

	var rows []storeInfo
	if unmarshalErr := json.Unmarshal(payload, &rows); unmarshalErr != nil {
		return fmt.Errorf("decode services file: %w", unmarshalErr)
	}

	s.rows = make([]Info, 0, len(rows))
	for _, row := range rows {
		info := row.toInfo()
		if info.Name == "" || info.Stack == "" {
			continue
		}
		s.rows = append(s.rows, info)
	}

	sortInfos(s.rows)
	s.reindexLocked()
	return nil
}

func (s *Store) flushLocked() error {
	payload, err := json.Marshal(storeInfosFromServiceInfos(s.rows))
	if err != nil {
		return fmt.Errorf("encode services file: %w", err)
	}

	tmpPath := fmt.Sprintf("%s.tmp", s.path)
	if writeErr := os.WriteFile(tmpPath, payload, fileModePrivate); writeErr != nil {
		return fmt.Errorf("write services temp file: %w", writeErr)
	}
	if renameErr := os.Rename(tmpPath, s.path); renameErr != nil {
		return fmt.Errorf("replace services file: %w", renameErr)
	}

	return nil
}

func (s *Store) reindexLocked() {
	s.byServiceNames = make(map[string]int, len(s.rows))
	for rowIndex, row := range s.rows {
		s.byServiceNames[serviceKey(row.Stack, row.Name)] = rowIndex
	}
}

func serviceKey(stackName string, serviceName string) string {
	return strings.TrimSpace(stackName) + "-" + strings.TrimSpace(serviceName)
}

func sortInfos(rows []Info) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Stack != rows[j].Stack {
			return rows[i].Stack < rows[j].Stack
		}

		return rows[i].Name < rows[j].Name
	})
}

type storeInfo struct {
	// KnownApp is a recognized application identifier.
	KnownApp knownapp.Name `json:"known_app"`
	// Description is a human-readable service description.
	Description string `json:"description"`
	// Type is a service classification.
	Type serviceType.Type `json:"type"`
	// RepositoryURL is a source repository URL resolved from service labels.
	RepositoryURL string `json:"repository_url"`
	// Links is a list of additional service-related links resolved from service labels.
	Links []metadata.Link `json:"links"`
	// Name is a service name inside stack.
	Name string `json:"name"`
	// Stack is a docker stack name.
	Stack string `json:"stack"`
	// Image is a service container image reference.
	Image string `json:"image"`
	// Environment is a resolved container environment snapshot.
	Environment map[string]string `json:"environment,omitempty"`
	// Spec is a compact persisted service spec snapshot.
	Spec swarm.ServiceSpec `json:"spec"`
	// WebRoutes is a list of public web routes resolved from service environment.
	WebRoutes []webroute.Route `json:"web_routes,omitempty"`
}

func (i storeInfo) toInfo() Info {
	return Info{
		Metadata: metadata.Metadata{
			KnownApp:      i.KnownApp,
			Description:   i.Description,
			Type:          i.Type,
			RepositoryURL: i.RepositoryURL,
			Links:         i.Links,
		},
		Name:        i.Name,
		Stack:       i.Stack,
		Image:       i.Image,
		Environment: i.Environment,
		Spec:        i.Spec,
		WebRoutes:   i.WebRoutes,
	}
}

func storeInfosFromServiceInfos(infos []Info) []storeInfo {
	rows := make([]storeInfo, 0, len(infos))
	for _, info := range infos {
		rows = append(rows, storeInfo{
			KnownApp:      info.KnownApp,
			Description:   info.Description,
			Type:          info.Type,
			RepositoryURL: info.RepositoryURL,
			Links:         info.Links,
			Name:          info.Name,
			Stack:         info.Stack,
			Image:         info.Image,
			Environment:   info.Environment,
			Spec:          info.Spec,
			WebRoutes:     info.WebRoutes,
		})
	}

	return rows
}
