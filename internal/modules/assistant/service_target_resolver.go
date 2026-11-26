package assistant

import (
	"context"
	"encoding/json/v2"
	"log/slog"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/llm"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
)

const targetResolverMaxTokens = 96

type serviceTargetResolver struct {
	texts     *texts.Localizer
	modelName string
	chat      modelCompleter
	store     ServiceStore
}

func newServiceTargetResolver(
	localizer *texts.Localizer,
	modelName string,
	chat modelCompleter,
	store ServiceStore,
) *serviceTargetResolver {
	return &serviceTargetResolver{
		texts:     localizer,
		modelName: modelName,
		chat:      chat,
		store:     store,
	}
}

func (r *serviceTargetResolver) resolve(
	ctx context.Context,
	target string,
) (serviceResolution, conversation.TokenUsage, error) {
	services, err := r.store.ReadAll(ctx)
	if err != nil {
		return serviceResolution{}, conversation.TokenUsage{}, err
	}
	resolution, usage := r.resolveFromServices(ctx, services, target)
	return resolution, usage, nil
}

type serviceResolution struct {
	stack     string
	service   string
	message   string
	selection *conversation.Selection
	ambiguous bool
	ok        bool
}

func (r *serviceTargetResolver) resolveDeterministic(
	services []model.Info,
	target string,
) serviceResolution {
	target = strings.TrimSpace(target)
	if target == "" {
		return serviceResolution{message: r.texts.Text("operation.ask_target")}
	}

	matches := model.MatchServiceTarget(services, target)
	switch len(matches) {
	case 0:
		return serviceResolution{
			message: r.texts.Format("operation.target_not_found", map[string]any{"Target": target}),
		}
	case 1:
		return resolvedService(matches[0])
	default:
		return r.ambiguousResolution(model.QualifiedNames(matches))
	}
}

func resolvedService(item model.Info) serviceResolution {
	return serviceResolution{stack: item.Stack, service: item.Name, ok: true}
}

func (r *serviceTargetResolver) ambiguousResolution(targets []string) serviceResolution {
	return serviceResolution{
		message:   r.texts.Text("operation.ambiguous_target"),
		selection: serviceTargetSelection(targets),
		ambiguous: true,
	}
}

func serviceTargetSelection(targets []string) *conversation.Selection {
	options := make([]conversation.SelectionOption, 0, len(targets))
	for _, target := range targets {
		stack, service, ok := strings.Cut(target, "/")
		option := conversation.SelectionOption{Label: target, Value: target}
		if ok {
			option.Label = service
			option.Description = stack
		}
		options = append(options, option)
	}
	return &conversation.Selection{Options: options}
}

type targetResolverDecision struct {
	Status     string   `json:"status"`
	Candidate  string   `json:"candidate,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

func (r *serviceTargetResolver) resolveFromServices(
	ctx context.Context,
	services []model.Info,
	target string,
) (serviceResolution, conversation.TokenUsage) {
	deterministic := r.resolveDeterministic(services, target)
	if deterministic.ok || deterministic.ambiguous || strings.TrimSpace(target) == "" || len(services) == 0 {
		return deterministic, conversation.TokenUsage{}
	}

	candidates := model.QualifiedNames(services)
	completion, err := r.chat.Complete(ctx, llm.Request{
		Model:       r.modelName,
		Temperature: 0,
		MaxTokens:   targetResolverMaxTokens,
		Messages: []llm.Message{
			{Role: "system", Content: targetResolverSystemPrompt},
			{
				Role:    "user",
				Content: "Target: " + strings.TrimSpace(target) + "\nCandidates:\n" + strings.Join(candidates, "\n"),
			},
		},
	})
	if err != nil {
		slog.WarnContext(ctx, "[assistant] service target resolver fallback failed", slog.Any("err", err))
		return deterministic, completion.Usage
	}

	resolution := r.validateDecision(services, completion.Content)
	if resolution.ok || resolution.message != "" {
		return resolution, completion.Usage
	}
	return deterministic, completion.Usage
}

func (r *serviceTargetResolver) validateDecision(
	services []model.Info,
	content string,
) serviceResolution {
	var decision targetResolverDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decision); err != nil {
		return serviceResolution{}
	}
	switch strings.ToLower(strings.TrimSpace(decision.Status)) {
	case "exact":
		for _, item := range services {
			if strings.EqualFold(item.QualifiedName(), strings.TrimSpace(decision.Candidate)) {
				return resolvedService(item)
			}
		}
	case "ambiguous":
		matches := targetResolverMatches(services, decision.Candidates)
		if len(matches) > 1 {
			return r.ambiguousResolution(model.QualifiedNames(matches))
		}
	case "not_found":
		return serviceResolution{}
	}
	return serviceResolution{}
}

func targetResolverMatches(services []model.Info, candidates []string) []model.Info {
	matches := make([]model.Info, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		for _, item := range services {
			canonical := item.QualifiedName()
			if !strings.EqualFold(canonical, strings.TrimSpace(candidate)) {
				continue
			}
			if _, ok := seen[canonical]; ok {
				continue
			}
			seen[canonical] = struct{}{}
			matches = append(matches, item)
		}
	}
	return matches
}

const targetResolverSystemPrompt = `Resolve one Docker Swarm service target against the provided candidates.
Return compact JSON only, using one form:
{"status":"exact","candidate":"stack/service"}
{"status":"ambiguous","candidates":["stack/service"]}
{"status":"not_found"}
Candidate values must be copied exactly from the provided list. Do not use tools or outside knowledge.`
