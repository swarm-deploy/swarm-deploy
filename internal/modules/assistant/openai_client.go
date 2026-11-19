package assistant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
)

const defaultOpenAIRequestTimeout = 60 * time.Second

type openAIClient struct {
	client openai.Client
}

func newOpenAIClient(baseURL, token, organizationID, customPrompt string) *openAIClient {
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)
	organizationID = strings.TrimSpace(organizationID)

	options := []option.RequestOption{
		option.WithAPIKey(token),
		option.WithRequestTimeout(defaultOpenAIRequestTimeout),
	}
	if baseURL != "" {
		options = append(options, option.WithBaseURL(baseURL))
	}
	if organizationID != "" {
		options = append(options, option.WithOrganization(organizationID))
	}
	if tracing.Enabled() {
		options = append(options, option.WithMiddleware(newOpenAITracing(customPrompt)))
	}

	return &openAIClient{
		client: openai.NewClient(options...),
	}
}

func (c *openAIClient) complete(ctx context.Context, req modelRequest) (modelResponse, error) {
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, message := range req.Messages {
		converted, err := toOpenAIMessage(message)
		if err != nil {
			return modelResponse{}, err
		}
		messages = append(messages, converted)
	}

	var tools []openai.ChatCompletionToolUnionParam
	if len(req.Tools) > 0 {
		tools = make([]openai.ChatCompletionToolUnionParam, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, openai.ChatCompletionToolUnionParam{
				OfFunction: &openai.ChatCompletionFunctionToolParam{
					Function: openai.FunctionDefinitionParam{
						Name:        strings.TrimSpace(string(tool.Name)),
						Description: openai.String(strings.TrimSpace(tool.Description)),
						Parameters:  tool.ParametersJSONSchema,
					},
				},
			})
		}
	}

	response, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:       strings.TrimSpace(req.Model),
		Messages:    messages,
		Temperature: openai.Float(req.Temperature),
		MaxTokens:   openai.Int(int64(req.MaxTokens)),
		Tools:       tools,
	})
	var result modelResponse
	if response != nil {
		result.Usage = providerTokenUsage(
			response.Usage.PromptTokens, response.Usage.CompletionTokens, response.Usage.TotalTokens,
		)
	}
	if err != nil {
		return result, safeChatCompletionError(err)
	}
	if response == nil || len(response.Choices) == 0 {
		return result, fmt.Errorf("chat completion response does not contain choices")
	}

	message := response.Choices[0].Message
	toolCalls := make([]modelToolCall, 0, len(message.ToolCalls))
	for _, toolCall := range message.ToolCalls {
		functionCall, ok := toolCall.AsAny().(openai.ChatCompletionMessageFunctionToolCall)
		if !ok {
			continue
		}
		toolCalls = append(toolCalls, modelToolCall{
			ID:        functionCall.ID,
			Name:      functionCall.Function.Name,
			Arguments: functionCall.Function.Arguments,
		})
	}

	result.Content = strings.TrimSpace(message.Content)
	result.ToolCalls = toolCalls
	return result, nil
}

// Provider and transport errors can include arbitrary response content and URLs.
func safeChatCompletionError(err error) error {
	var apiError *openai.Error
	switch {
	case errors.As(err, &apiError):
		switch apiError.Code {
		case "model_not_found", "unsupported_parameter", "unsupported_value", "context_length_exceeded",
			"invalid_api_key", "insufficient_quota", "rate_limit_exceeded":
			return fmt.Errorf("chat completion request failed with HTTP status %d (%s)", apiError.StatusCode, apiError.Code)
		}
		return fmt.Errorf("chat completion request failed with HTTP status %d", apiError.StatusCode)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("chat completion request failed: %w", context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("chat completion request failed: %w", context.DeadlineExceeded)
	default:
		return errors.New("chat completion request failed")
	}
}

func (c *openAIClient) Embed(
	ctx context.Context, model string, inputs []string,
) ([][]float64, conversation.TokenUsage, error) {
	response, err := c.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: strings.TrimSpace(model),
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: inputs,
		},
	})
	var usage conversation.TokenUsage
	if response != nil {
		usage = providerTokenUsage(response.Usage.PromptTokens, 0, response.Usage.TotalTokens)
	}
	if err != nil {
		return nil, usage, fmt.Errorf("embeddings request failed: %w", err)
	}

	if response == nil {
		return nil, usage, fmt.Errorf("unexpected embeddings size: got 0, expected %d", len(inputs))
	}
	if len(response.Data) != len(inputs) {
		return nil, usage, fmt.Errorf("unexpected embeddings size: got %d, expected %d", len(response.Data), len(inputs))
	}

	slices.SortFunc(response.Data, func(left, right openai.Embedding) int {
		return int(left.Index - right.Index)
	})

	vectors := make([][]float64, 0, len(response.Data))
	for idx, item := range response.Data {
		if int(item.Index) != idx {
			return nil, usage, fmt.Errorf("unexpected embeddings index %d at position %d", item.Index, idx)
		}
		if len(item.Embedding) == 0 {
			return nil, usage, fmt.Errorf("empty embedding at index %d", idx)
		}
		vectors = append(vectors, item.Embedding)
	}

	return vectors, usage, nil
}

func providerTokenUsage(input, output, total int64) conversation.TokenUsage {
	if total == 0 {
		total = input + output
	}
	return conversation.TokenUsage{InputTokens: input, OutputTokens: output, TotalTokens: total}
}

func toOpenAIMessage(message modelMessage) (openai.ChatCompletionMessageParamUnion, error) {
	switch strings.ToLower(strings.TrimSpace(message.Role)) {
	case "system":
		return openai.SystemMessage(message.Content), nil
	case "user":
		return openai.UserMessage(message.Content), nil
	case "assistant":
		assistantMessage := openai.ChatCompletionAssistantMessageParam{}
		if strings.TrimSpace(message.Content) != "" {
			assistantMessage.Content.OfString = openai.String(message.Content)
		}
		if strings.TrimSpace(message.Name) != "" {
			assistantMessage.Name = openai.String(strings.TrimSpace(message.Name))
		}
		for _, toolCall := range message.ToolCalls {
			assistantMessage.ToolCalls = append(
				assistantMessage.ToolCalls,
				openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: strings.TrimSpace(toolCall.ID),
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      strings.TrimSpace(toolCall.Name),
							Arguments: toolCall.Arguments,
						},
					},
				},
			)
		}
		return openai.ChatCompletionMessageParamUnion{
			OfAssistant: &assistantMessage,
		}, nil
	case "tool":
		toolCallID := strings.TrimSpace(message.ToolCallID)
		if toolCallID == "" {
			return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("tool message requires tool_call_id")
		}
		return openai.ToolMessage(message.Content, toolCallID), nil
	case "function":
		name := strings.TrimSpace(message.Name)
		if name == "" {
			return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("function message requires name")
		}
		return openai.ChatCompletionMessageParamOfFunction(message.Content, name), nil
	default:
		return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("unsupported message role %q", message.Role)
	}
}
