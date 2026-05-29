package statem

import (
	"log/slog"
	"time"
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

func (s *WarmupStore) Get() Runtime {
	return s.hot.Get()
}

func (s *WarmupStore) Stop() {
	s.stop <- struct{}{}

	s.hot.Stop()
	s.cold.Stop()
}

func (s *WarmupStore) Update(fn func(*Runtime)) {
	s.hot.Update(fn)
}

func (s *WarmupStore) Warmup() {
	val := s.cold.Get()

	s.hot.Update(func(runtime *Runtime) {
		*runtime = val
	})
}

const syncInterval = 2 * time.Second

func (s *WarmupStore) Sync() {
	last := s.cold.Get()

	for {
		select {
		case <-s.stop:
			slog.Info("[state-warmup-store] sync stopped")
			return
		case <-time.Tick(syncInterval):
			val := s.hot.Get()
			if val.LastSyncAt.Equal(last.LastSyncAt) {
				continue
			}

			s.cold.Update(func(runtime *Runtime) {
				*runtime = val
			})
		}
	}
}
