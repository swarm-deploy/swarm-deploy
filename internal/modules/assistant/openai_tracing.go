package assistant

import (
	"encoding/json"
	"net/http"
	"strings"

	langwatch "github.com/langwatch/langwatch/sdks/go"
	otelopenai "github.com/langwatch/langwatch/sdks/go/instrumentation/openai"
	"github.com/langwatch/langwatch/sdks/go/instrumentation/openaiformat"
	"github.com/langwatch/langwatch/sdks/go/instrumentation/otelhttp"
	"github.com/openai/openai-go/v3/option"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

const configurationToolName = string(config.AssistantToolNameSwarmDeployConfigGet)

func newOpenAITracing(customPrompt string) option.Middleware {
	extractor := configurationTraceExtractor{customPrompt: strings.TrimSpace(customPrompt)}
	chatTracer := otelhttp.New(otelhttp.Config{
		TracerName: "github.com/langwatch/langwatch/sdks/go/instrumentation/openai",
		Provider:   semconv.GenAIProviderNameOpenAI,
		Extractors: []otelhttp.Extractor{extractor},
		OperationAttrs: func(req *http.Request) []attribute.KeyValue {
			return openaiformat.OperationAttrs(req.URL.Path)
		},
	})
	otherEndpoints := otelopenai.Middleware("swarm-deploy")
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/chat/completions") {
			var transportErr error
			response, err := chatTracer.Handle(req, func(req *http.Request) (*http.Response, error) {
				response, err := next(req)
				if err != nil {
					transportErr = err
					return response, safeChatCompletionError(err)
				}
				return response, nil
			})
			if transportErr != nil {
				return response, transportErr
			}
			return response, err
		}
		return otherEndpoints(req, next)
	}
}

// The extractor receives a telemetry copy of the HTTP body. The provider and
// the application still receive the original request and response respectively.
type configurationTraceExtractor struct {
	openaiformat.ChatExtractor
	customPrompt string
}

func (e configurationTraceExtractor) ExtractRequest(
	span *langwatch.Span, raw []byte, capture langwatch.DataCaptureMode,
) bool {
	if capture.CaptureInput() {
		raw, capture = redactChatTrace(raw, capture, e.redactRequest)
	}
	return e.ChatExtractor.ExtractRequest(span, raw, capture)
}

func (e configurationTraceExtractor) ExtractNonStreaming(
	span *langwatch.Span, raw []byte, capture langwatch.DataCaptureMode,
) {
	if capture.CaptureOutput() {
		raw, capture = redactChatTrace(raw, capture, redactCompletionTrace)
	}
	e.ChatExtractor.ExtractNonStreaming(span, raw, capture)
}

func (configurationTraceExtractor) NewStreamAccumulator() otelhttp.StreamAccumulator {
	// The assistant uses non-streaming completions. Ignore unexpected streams.
	return otelhttp.NoopAccumulator{}
}

func redactChatTrace(
	raw []byte, capture langwatch.DataCaptureMode, redact func(otelhttp.JSONObject) bool,
) ([]byte, langwatch.DataCaptureMode) {
	body, ok := otelhttp.ParseBody(raw)
	if !ok {
		return raw, langwatch.DataCaptureNone
	}
	if !redact(body) {
		return raw, capture
	}
	redacted, err := json.Marshal(body)
	if err != nil {
		return raw, langwatch.DataCaptureNone
	}
	return redacted, capture
}

func (e configurationTraceExtractor) redactRequest(body otelhttp.JSONObject) bool {
	messages, _ := body["messages"].([]any)
	configCallIDs := make(map[string]struct{})
	changed := false
	for _, item := range messages {
		message, _ := item.(otelhttp.JSONObject)
		switch message["role"] {
		case "assistant":
			changed = redactConfigurationCalls(message, configCallIDs) || changed
		case "tool":
			id, _ := message["tool_call_id"].(string)
			if _, ok := configCallIDs[id]; ok {
				message["content"] = "[configuration result omitted]"
				changed = true
			}
		case "system":
			changed = e.redactCustomPrompt(message) || changed
		}
	}
	return changed
}

func (e configurationTraceExtractor) redactCustomPrompt(message otelhttp.JSONObject) bool {
	if e.customPrompt == "" {
		return false
	}
	content, _ := message["content"].(string)
	if prefix, ok := strings.CutSuffix(content, customPromptHeading+e.customPrompt); ok {
		message["content"] = prefix
		return true
	}
	return false
}

func redactCompletionTrace(body otelhttp.JSONObject) bool {
	choices, _ := body["choices"].([]any)
	changed := false
	for _, item := range choices {
		choice, _ := item.(otelhttp.JSONObject)
		message, _ := choice["message"].(otelhttp.JSONObject)
		changed = redactConfigurationCalls(message, nil) || changed
	}
	return changed
}

func redactConfigurationCalls(message otelhttp.JSONObject, ids map[string]struct{}) bool {
	calls, _ := message["tool_calls"].([]any)
	changed := false
	for _, item := range calls {
		call, _ := item.(otelhttp.JSONObject)
		function, _ := call["function"].(otelhttp.JSONObject)
		name, _ := function["name"].(string)
		if !isConfigurationTool(name) {
			continue
		}
		function["arguments"] = "{}"
		changed = true
		if id, ok := call["id"].(string); ok && ids != nil {
			ids[id] = struct{}{}
		}
	}
	return changed
}
