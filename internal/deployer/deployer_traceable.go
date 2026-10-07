package deployer

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TraceableDeployer struct {
	tracer   trace.Tracer
	deployer StackDeployer
}

func newTraceableDeployer(tp trace.TracerProvider, deployer StackDeployer) StackDeployer {
	return &TraceableDeployer{
		tracer:   tp.Tracer("github.com/swarm-deploy/swarm-deploy/internal/deployer"),
		deployer: deployer,
	}
}

func (t TraceableDeployer) DeployStack(
	ctx context.Context,
	stackName,
	sourceComposePath,
	deployComposePath string,
	desired compose.Compose,
) error {
	ctx, span := t.tracer.Start(ctx, "deployer.DeployStack", trace.WithAttributes(
		tracing.ResourceStackName.String(stackName),
		tracing.ResourceComposePath.String(deployComposePath),
	))
	defer span.End()

	err := t.deployer.DeployStack(ctx, stackName, sourceComposePath, deployComposePath, desired)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}

func (t TraceableDeployer) DeployService(
	ctx context.Context,
	stackName,
	composePath string,
	service compose.Service,
) error {
	ctx, span := t.tracer.Start(ctx, "deployer.DeployService", trace.WithAttributes(
		tracing.ResourceStackName.String(stackName),
		tracing.ResourceComposePath.String(composePath),
	))
	defer span.End()

	err := t.deployer.DeployService(ctx, stackName, composePath, service)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
