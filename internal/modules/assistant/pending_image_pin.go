package assistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
)

func (t *operationTurn) collectImagePin(
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
	resolution, usage, err := t.serviceTargets.resolve(ctx, target)
	if err != nil {
		t.pending.delete(t.conversationID)
		return t.texts.Text("operation.catalog_failed"), usage
	}
	if !resolution.ok {
		t.next = &op
		t.selection = resolution.selection
		return resolution.message, usage
	}
	return t.prepareImagePinOperation(ctx, op, resolution.stack, resolution.service), usage
}

func (t *operationTurn) prepareImagePinOperation(
	ctx context.Context, op PendingOperation, stack, service string,
) string {
	assessment, err := t.prepareImagePin(ctx, stack, service)
	if err != nil {
		t.pending.delete(t.conversationID)
		return t.texts.Format("operation.failed", map[string]any{"Error": err.Error()})
	}
	data := imagePinAssessmentData(assessment)
	switch assessment.Status {
	case usecase.ServiceImagePinReady:
		op.ImagePinPlan = assessment.Plan
		return t.confirmOperation(op)
	case usecase.ServiceImagePinAlreadyPinned:
		t.pending.delete(t.conversationID)
		return t.texts.Format("result.image_pin_already", data)
	case usecase.ServiceImagePinMixedRollout:
		t.pending.delete(t.conversationID)
		return t.texts.Format("result.image_pin_mixed", data)
	case usecase.ServiceImagePinNoRunningTasks:
		t.pending.delete(t.conversationID)
		return t.texts.Format("result.image_pin_no_running", data)
	case usecase.ServiceImagePinDigestUnavailable:
		t.pending.delete(t.conversationID)
		return t.texts.Format("result.image_pin_digest_unavailable", data)
	case usecase.ServiceImagePinGitMismatch:
		t.pending.delete(t.conversationID)
		return t.texts.Format("result.image_pin_mismatch", data)
	default:
		t.pending.delete(t.conversationID)
		return t.texts.Text("operation.service_changed")
	}
}

func imagePinMessageData(plan usecase.ServiceImagePinPlan) map[string]any {
	return map[string]any{
		"Stack": plan.Stack, "Service": plan.Service, "Count": plan.RunningTasks,
		"CurrentImage": plan.CurrentImage, "ProposedImage": plan.ProposedImage,
	}
}

func imagePinAssessmentData(assessment usecase.ServiceImagePinAssessment) map[string]any {
	data := imagePinMessageData(assessment.Plan)
	data["Digests"] = formatImageCounts(assessment.DigestCounts)
	data["Images"] = formatImageCounts(assessment.ImageCounts)
	data["InvalidCount"] = assessment.InvalidTasks
	return data
}

func formatImageCounts(counts []usecase.ImageCount) string {
	items := make([]string, 0, len(counts))
	for _, item := range counts {
		value := item.Value
		if strings.TrimSpace(value) == "" {
			value = "<empty>"
		}
		items = append(items, fmt.Sprintf("`%s`: %d", value, item.Count))
	}
	return strings.Join(items, ", ")
}
