package rag

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/artarts36/swarm-deploy/internal/event/events"
)

// IndexSubscriber updates embeddings index on deploy success events.
type IndexSubscriber struct {
	store     ServiceStore
	embedder  Embedder
	modelName string
	index     *Index
	observer  Observer
}

// NewIndexSubscriber creates deploySuccess subscriber for RAG index updates.
func NewIndexSubscriber(
	store ServiceStore,
	embedder Embedder,
	modelName string,
	index *Index,
	observer Observer,
) *IndexSubscriber {
	if index == nil {
		index = NewIndex()
	}

	return &IndexSubscriber{
		store:     store,
		embedder:  embedder,
		modelName: strings.TrimSpace(modelName),
		index:     index,
		observer:  observer,
	}
}

// Name returns subscriber identifier.
func (*IndexSubscriber) Name() string {
	return "assistant-rag-index"
}

// Handle rebuilds embeddings index after deploySuccess events.
func (s *IndexSubscriber) Handle(ctx context.Context, event events.Event) error {
	if _, ok := event.(*events.DeploySuccess); !ok {
		return nil
	}

	startedAt := time.Now()
	services := s.store.List()
	if len(services) == 0 {
		s.index.Clear()
		slog.InfoContext(ctx, "[assistant-rag] cleared index after deploySuccess: no services")
		s.recordRebuild("empty", 0, startedAt, time.Now())
		return nil
	}

	documents := make([]string, 0, len(services))
	for _, serviceInfo := range services {
		documents = append(documents, serviceToDocument(serviceInfo))
	}

	embeddings, err := s.embedder.Embed(ctx, s.modelName, documents)
	if err != nil {
		s.recordRebuild("error", 0, startedAt, time.Now())
		slog.WarnContext(ctx, "[assistant-rag] failed to rebuild index embeddings", slog.Any("err", err))
		return fmt.Errorf("build rag embeddings: %w", err)
	}

	if replaceErr := s.index.Replace(services, embeddings); replaceErr != nil {
		s.recordRebuild("error", 0, startedAt, time.Now())
		slog.WarnContext(ctx, "[assistant-rag] failed to replace index snapshot", slog.Any("err", replaceErr))
		return fmt.Errorf("update rag index: %w", replaceErr)
	}

	updatedAt := time.Now()
	slog.InfoContext(
		ctx,
		"[assistant-rag] rebuilt embeddings index",
		slog.Int("services", len(services)),
		slog.Duration("duration", time.Since(startedAt)),
	)
	s.recordRebuild("success", len(services), startedAt, updatedAt)

	return nil
}

func (s *IndexSubscriber) recordRebuild(status string, size int, startedAt time.Time, updatedAt time.Time) {
	if s.observer == nil {
		return
	}

	s.observer.RecordIndexRebuild(status, size, time.Since(startedAt), updatedAt)
}
