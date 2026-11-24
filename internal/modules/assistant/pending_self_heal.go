package assistant

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
)

func (t *operationTurn) collectSelfHeal(
	ctx context.Context, op PendingOperation, target string,
) (string, conversation.TokenUsage) {
	var usage conversation.TokenUsage
	if !t.operationAllowed(CapabilityServiceOperations, op.Type) {
		t.pending.delete(t.conversationID)
		return t.texts.Text("operation.forbidden"), usage
	}
	if target == "" {
		t.next = &op
		return t.texts.Text("operation.ask_target"), usage
	}
	plan, err := t.prepareSelfHeal(ctx, target)
	if errors.Is(err, usecase.ErrServiceNotFound) {
		// Natural-language fallback belongs to the chat. Prepare the chosen catalog name again.
		resolution, tokens := t.lookupService(ctx, target)
		usage = tokens
		if !resolution.ok {
			t.next = &op
			return resolution.message, usage
		}
		plan, err = t.prepareSelfHeal(ctx, resolution.stack+"/"+resolution.service)
	}
	var ambiguous *usecase.AmbiguousServiceTargetError
	if errors.As(err, &ambiguous) {
		t.next = &op
		return t.ambiguousServiceResolution(ambiguous.Candidates).message, usage
	}
	if err != nil {
		t.pending.delete(t.conversationID)
		if errors.Is(err, usecase.ErrServiceCatalogUnavailable) {
			return t.texts.Text("operation.catalog_failed"), usage
		}
		return t.texts.Format("operation.failed", map[string]any{"Error": err.Error()}), usage
	}
	if plan.Policy.Enabled {
		t.pending.delete(t.conversationID)
		return t.texts.Format("result.self_heal_enabled", t.selfHealMessageData(plan)), usage
	}
	op.SelfHealPlan = plan
	return t.confirmOperation(op), usage
}

func (f *operationFlow) selfHealMessageData(plan usecase.ServiceSelfHealPlan) map[string]any {
	return map[string]any{
		"Stack": plan.Stack, "Service": plan.Service, "Source": f.texts.Text("policy.source_" + plan.Policy.Source),
	}
}
