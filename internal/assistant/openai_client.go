package assistant

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const defaultOpenAIRequestTimeout = 60 * time.Second

type openAIClient struct {
	baseURL string
	client  openai.Client
}

func newOpenAIClient(baseURL, token, organizationID string) *openAIClient {
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

	return &openAIClient{
		baseURL: baseURL,
		client:  openai.NewClient(options...),
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
						Name:        strings.TrimSpace(tool.Name),
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
	if err != nil {
		return modelResponse{}, fmt.Errorf("chat completion request failed: %w", err)
	}
	if response == nil || len(response.Choices) == 0 {
		return modelResponse{}, fmt.Errorf("chat completion response does not contain choices")
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

	return modelResponse{
		Content:   strings.TrimSpace(message.Content),
		ToolCalls: toolCalls,
	}, nil
}

func (c *openAIClient) Embed(ctx context.Context, model string, inputs []string) ([][]float64, error) {
	response, err := c.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: strings.TrimSpace(model),
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: inputs,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("embeddings request failed: %w", err)
	}

	if response == nil {
		return nil, fmt.Errorf("unexpected embeddings size: got 0, expected %d", len(inputs))
	}
	if len(response.Data) != len(inputs) {
		return nil, fmt.Errorf("unexpected embeddings size: got %d, expected %d", len(response.Data), len(inputs))
	}

	slices.SortFunc(response.Data, func(left, right openai.Embedding) int {
		return int(left.Index - right.Index)
	})

	vectors := make([][]float64, 0, len(response.Data))
	for idx, item := range response.Data {
		if int(item.Index) != idx {
			return nil, fmt.Errorf("unexpected embeddings index %d at position %d", item.Index, idx)
		}
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("empty embedding at index %d", idx)
		}
		vectors = append(vectors, item.Embedding)
	}

	return vectors, nil
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
