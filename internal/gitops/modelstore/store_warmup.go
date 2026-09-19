package modelstore

import (
	"context"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
)

type WarmupStore struct {
	hot  Store
	cold Store

	stop chan struct{}
}

func NewWarmupStore(hot Store, cold Store) *WarmupStore {
	s := &WarmupStore{
		hot:  hot,
		cold: cold,
		stop: make(chan struct{}, 1),
	}

	return s
}

func (s *WarmupStore) Get() model.Runtime {
	return s.hot.Get()
}

func (s *WarmupStore) Stop() {
	s.stop <- struct{}{}

	s.hot.Stop()
	s.cold.Stop()
}

func (s *WarmupStore) Update(ctx context.Context, fn func(*model.Runtime)) {
	s.hot.Update(ctx, fn)
}

func (s *WarmupStore) Warmup(ctx context.Context) {
	val := s.cold.Get()

	s.hot.Update(ctx, func(runtime *model.Runtime) {
		*runtime = val
	})
}

const syncInterval = 2 * time.Second

func (s *WarmupStore) Sync(ctx context.Context) {
	last := s.cold.Get()

	for {
		select {
		case <-s.stop:
			slog.InfoContext(ctx, "[state-warmup-store] sync stopped")
			return
		case <-time.Tick(syncInterval):
			val := s.hot.Get()
			if val.LastSyncAt.Equal(last.LastSyncAt) {
				continue
			}

			s.cold.Update(ctx, func(runtime *model.Runtime) {
				*runtime = val
				last = runtime.Clone()
			})
		}
	}
}
