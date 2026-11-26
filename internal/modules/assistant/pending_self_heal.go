package assistant

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
)

func (op OperationType) isSelfHeal() bool {
	return op == OperationServiceSelfHealEnable || op == OperationServiceSelfHealDisable
}

func (f *operationFlow) selfHealPreparation(
	op OperationType,
) func(context.Context, string, string) (usecase.ServiceSelfHealPlan, error) {
	if op == OperationServiceSelfHealDisable {
		return f.prepareSelfHealDisable
	}
	return f.prepareSelfHeal
}

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
	resolution, usage, err := t.lookupService(ctx, target)
	if err != nil {
		t.pending.delete(t.conversationID)
		return t.texts.Text("operation.catalog_failed"), usage
	}
	if !resolution.ok {
		t.next = &op
		t.selection = resolution.selection
		return resolution.message, usage
	}
	return t.prepareSelfHealOperation(ctx, op, resolution.stack, resolution.service), usage
}

func (t *operationTurn) prepareSelfHealOperation(
	ctx context.Context, op PendingOperation, stack, service string,
) string {
	plan, err := t.selfHealPreparation(op.Type)(ctx, stack, service)
	if err != nil {
		t.pending.delete(t.conversationID)
		if errors.Is(err, usecase.ErrServiceCatalogUnavailable) {
			return t.texts.Text("operation.catalog_failed")
		}
		return t.texts.Format("operation.failed", map[string]any{"Error": err.Error()})
	}
	if plan.Policy.Enabled == (op.Type == OperationServiceSelfHealEnable) {
		t.pending.delete(t.conversationID)
		message := "result.self_heal_enabled"
		if op.Type == OperationServiceSelfHealDisable {
			message = "result.self_heal_disabled"
		}
		return t.texts.Format(message, t.selfHealMessageData(plan))
	}
	op.SelfHealPlan = plan
	return t.confirmOperation(op)
}

func (f *operationFlow) selfHealMessageData(plan usecase.ServiceSelfHealPlan) map[string]any {
	return map[string]any{
		"Stack": plan.Stack, "Service": plan.Service, "Source": f.texts.Text("policy.source_" + plan.Policy.Source),
	}
}
