package authenticator

import (
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
)

type traceStartAuthenticator struct {
	tracer        trace.Tracer
	authenticator Authenticator
}

type traceSpanAuthenticator struct {
	tracer        trace.Tracer
	authenticator Authenticator
	name          config.WebhookAuthType
}

func newTraceStartAuthenticator(
	tracerProvider trace.TracerProvider,
	authenticator Authenticator,
) *traceStartAuthenticator {
	return &traceStartAuthenticator{
		tracer:        tracerProvider.Tracer("github.com/swarm-deploy/entrypoints/webhookserver/authenticator"),
		authenticator: authenticator,
	}
}

func newTraceSpanAuthenticator(
	tracerProvider trace.TracerProvider,
	authenticator Authenticator,
	name config.WebhookAuthType,
) *traceSpanAuthenticator {
	return &traceSpanAuthenticator{
		tracer:        tracerProvider.Tracer("github.com/swarm-deploy/entrypoints/webhookserver/authenticator"),
		authenticator: authenticator,
		name:          name,
	}
}

func (t *traceStartAuthenticator) Authenticate(req *Request) error {
	ctx, span := t.tracer.Start(req.Request.Context(), "webhook.Authenticate")
	defer span.End()

	req.Request = req.Request.WithContext(ctx)

	err := t.authenticator.Authenticate(req)
	if err != nil {
		span.SetAttributes(
			tracing.WebhookAuthRequestAllowed(false),
		)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return err
	}

	span.SetAttributes(
		tracing.WebhookAuthRequestAllowed(true),
	)

	return nil
}

func (t *traceSpanAuthenticator) Authenticate(req *Request) error {
	span := trace.SpanFromContext(req.Request.Context())
	if !span.IsRecording() {
		return t.authenticator.Authenticate(req)
	}

	err := t.authenticator.Authenticate(req)
	if err != nil {
		span.AddEvent(fmt.Sprintf("%q: not passed", t.name), trace.WithAttributes(
			tracing.WebhookAuthAuthenticatorName(string(t.name)),
			tracing.WebhookAuthRequestAllowed(false),
			tracing.WebhookAuthRequestCredentialsProvided(!errors.Is(err, ErrValueNotProvided)),
		))
		return err
	}

	span.AddEvent(fmt.Sprintf("%q: passed", t.name), trace.WithAttributes(
		tracing.WebhookAuthAuthenticatorName(string(t.name)),
		tracing.WebhookAuthRequestAllowed(true),
		tracing.WebhookAuthRequestCredentialsProvided(true),
	))

	return nil
}
