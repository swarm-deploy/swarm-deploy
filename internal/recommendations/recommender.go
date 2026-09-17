package recommendations

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/analyzer"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/modelstore"
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

func (r *Recommender) Recommend(ctx context.Context, stackName string, stack compose.File) error {
	recs := r.analyzers.Analyze(ctx, stack)
	if len(recs) == 0 {
		return nil
	}

	err := r.store.UpdateStack(ctx, stackName, recs)
	if err != nil {
		return fmt.Errorf("update stack recommendations: %w", err)
	}

	return nil
}
