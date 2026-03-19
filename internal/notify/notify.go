package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Image struct {
	FullName string `json:"full_name"`
	Version  string `json:"version"`
}

type Event struct {
	Status    string    `json:"status"`
	StackName string    `json:"stack_name"`
	Service   string    `json:"service"`
	Image     Image     `json:"image"`
	Commit    string    `json:"commit,omitempty"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type Notifier interface {
	Name() string
	Notify(ctx context.Context, event Event) error
}

type Manager struct {
	notifiers []Notifier
}

func NewManager(notifiers ...Notifier) *Manager {
	return &Manager{notifiers: notifiers}
}

func (m *Manager) Notify(ctx context.Context, event Event) error {
	if len(m.notifiers) == 0 {
		return nil
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, n := range m.notifiers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := n.Notify(ctx, event); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", n.Name(), err))
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return errors.Join(errs...)
}
