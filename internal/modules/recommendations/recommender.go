package recommendations

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/analyzer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
)

type Recommender struct {
	analyzers analyzer.StackAnalyzer
	store     modelstore.Store
}

func NewRecommender(
	analyzers analyzer.StackAnalyzer,
	store modelstore.Store,
) *Recommender {
	return &Recommender{
		analyzers: analyzers,
		store:     store,
	}
}

func (r *Recommender) Recommend(ctx context.Context, stack model.Stack) error {
	recs := r.analyzers.Analyze(ctx, stack)

	err := r.store.UpdateStack(ctx, stack.Name, recs)
	if err != nil {
		return fmt.Errorf("update stack recommendations: %w", err)
	}

	return nil
}
