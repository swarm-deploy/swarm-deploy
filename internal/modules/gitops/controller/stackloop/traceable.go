package stackloop

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
)

type TraceableReconciler struct {
	tracer     trace.Tracer
	reconciler StackReconciler
}

func NewTraceableReconciler(tp trace.TracerProvider, reconciler StackReconciler) *TraceableReconciler {
	return &TraceableReconciler{
		tracer:     tp.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"),
		reconciler: reconciler,
	}
}

func (t *TraceableReconciler) Cleanup(ctx context.Context, stack config.StackSpec) error {
	ctx, span := t.tracer.Start(ctx, "stackReconciler.Cleanup", trace.WithAttributes(
		tracing.ResourceStackName.String(stack.Name),
	))
	defer span.End()

	err := t.reconciler.Cleanup(ctx, stack)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (t *TraceableReconciler) Reconcile(ctx context.Context, req ReconciliationRequest) error {
	ctx, span := t.tracer.Start(ctx, "stackReconciler.Reconcile", trace.WithAttributes(
		tracing.ResourceStackName.String(req.Stack.Name),
		tracing.SyncCommitSha.String(req.Commit),
	))
	defer span.End()

	err := t.reconciler.Reconcile(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}
