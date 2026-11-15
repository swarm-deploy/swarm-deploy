package stackloop

import (
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

func targetServices(desired *compose.File, previous model.Stack) ([]string, map[string]string) {
	names := make([]string, 0, len(desired.Compose.Services))
	commits := make(map[string]string)
	for _, service := range desired.Compose.Services {
		state := previous.Services[service.Name]
		if state.ApplyPending || state.AppliedDigest == "" || state.DesiredDigest != state.AppliedDigest {
			names = append(names, service.Name)
			commits[service.Name] = state.DesiredCommit
		}
	}
	return names, commits
}

// adoptLegacyApplied treats a service synchronized before applied revisions were
// tracked as applied at the stack's last commit, so an upgrade does not redeploy it.
func adoptLegacyApplied(stack model.Stack, service model.Service, sourceDigest, digest string) model.Service {
	legacy := service.AppliedDigest == "" && !service.ApplyPending && service.SyncStatus == model.SyncStatusSynced
	if !legacy || stack.LastCommit == "" {
		return service
	}
	switch {
	case service.DesiredDigest != "":
		// The synchronized service matches the desired digest tracked before execution.
		service.AppliedDigest = service.DesiredDigest
	case stack.SourceDigest != "" && stack.SourceDigest == sourceDigest:
		// The source is unchanged, so the current effective state is the synchronized one.
		service.DesiredDigest, service.DesiredCommit = digest, stack.LastCommit
		service.AppliedDigest = digest
	default:
		// The source changed with the upgrade, so the synchronized revision is unknown.
		return service
	}
	service.AppliedCommit = stack.LastCommit
	return service
}

func serviceExecutionState(
	service compose.Service, previous model.Service, payload *pipelinePayload, now time.Time,
) model.Service {
	state := previous
	state.SyncStatus, state.SyncError = model.SyncStatusSynced, ""
	targeted := payload.Attempt != nil && payload.Attempt.Targets(service.Name)
	state.SelfHealPending = !targeted && payload.PendingSelfHeal[service.Name]
	if revision, ok := payload.AppliedRevisions[service.Name]; ok {
		state.AppliedDigest, state.AppliedCommit = revision.Digest, revision.Commit
		state.ApplyPending = revision.Digest != state.DesiredDigest
		state.Image = service.Image
		// Replayed receipts do not represent a fresh execution.
		if targeted {
			state.SyncAt = now
		}
	}
	if state.ApplyPending || state.AppliedDigest != state.DesiredDigest {
		state.SyncStatus = model.SyncStatusOutOfSync
	}
	return state
}
