package swarm

import (
	"context"

	dockerregistry "github.com/docker/docker/api/types/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type traceableImageManager struct {
	tracer       trace.Tracer
	imageManager ImageManager
}

func traceImageManager(tracer trace.Tracer, imageManager ImageManager) ImageManager {
	return &traceableImageManager{
		tracer:       tracer,
		imageManager: imageManager,
	}
}

func (t traceableImageManager) Get(ctx context.Context, imageRef string) (Image, error) {
	ctx, span := t.tracer.Start(ctx, "swarm.GetImage", trace.WithAttributes(
		tracing.ResourceImageRef.String(imageRef),
	))
	defer span.End()

	image, err := t.imageManager.Get(ctx, imageRef)
	if err != nil {
		tracing.FailSpan(span, err)
		return Image{}, err
	}

	span.SetAttributes(tracing.ResourceImageID.String(image.ID))
	span.SetStatus(codes.Ok, "")

	return image, nil
}

func (t traceableImageManager) InspectDistribution(
	ctx context.Context, imageRef, encodedRegistryAuth string,
) (dockerregistry.DistributionInspect, error) {
	ctx, span := t.tracer.Start(ctx, "swarm.InspectDistribution", trace.WithAttributes(
		tracing.ResourceImageRef.String(imageRef),
	))
	defer span.End()

	result, err := t.imageManager.InspectDistribution(ctx, imageRef, encodedRegistryAuth)
	if err != nil {
		tracing.FailSpan(span, err)
		return dockerregistry.DistributionInspect{}, err
	}

	span.SetStatus(codes.Ok, "")

	return result, nil
}
