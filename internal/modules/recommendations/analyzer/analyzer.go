package analyzer

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

type StackAnalyzer interface {
	Analyze(ctx context.Context, stack model.Stack) []model.Recommendation
}

type compositeAnalyzer struct {
	analyzers []StackAnalyzer
}

func Composite(analyzers ...StackAnalyzer) StackAnalyzer {
	return &compositeAnalyzer{
		analyzers: analyzers,
	}
}

func (a *compositeAnalyzer) Analyze(ctx context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, analyzer := range a.analyzers {
		recs = append(recs, analyzer.Analyze(ctx, stack)...)
	}

	return recs
}
