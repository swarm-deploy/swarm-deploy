package deployer

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TraceableDeployer struct {
	tracer   trace.Tracer
	deployer StackDeployer
}

func newTraceableDeployer(tp trace.TracerProvider, deployer StackDeployer) StackDeployer {
	return &TraceableDeployer{
		tracer:   tp.Tracer("github.com/swarm-deploy/swarm-deploy/deployer"),
		deployer: deployer,
	}
}

func (t TraceableDeployer) DeployStack(
	ctx context.Context,
	stackName,
	composePath string,
	services []compose.Service,
) error {
	ctx, span := t.tracer.Start(ctx, "deployer.DeployStack", trace.WithAttributes(
		tracing.ResourceStackName.String(stackName),
		tracing.ResourceComposePath.String(composePath),
	))
	defer span.End()

	err := t.deployer.DeployStack(ctx, stackName, composePath, services)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}
