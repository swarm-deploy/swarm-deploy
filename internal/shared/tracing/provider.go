package tracing

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var tracingEnabled = false

func GetTracerProvider() (trace.TracerProvider, bool) {
	return otel.GetTracerProvider(), tracingEnabled
}

func Enable() {
	tracingEnabled = true
}

func Enabled() bool {
	return tracingEnabled
}
