package notifiers

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type Image struct {
	FullName string `json:"full_name"`
	Version  string `json:"version"`
}

type Message struct {
	Payload any `json:",inline"`
}

type Notifier interface {
	Name() string
	Notify(ctx context.Context, event Message) error
}

type Manager struct {
	notifiers []Notifier
}

func NewManager(notifiers ...Notifier) *Manager {
	return &Manager{notifiers: notifiers}
}

func (m *Manager) Notify(ctx context.Context, event Message) error {
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
