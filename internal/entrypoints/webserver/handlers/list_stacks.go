package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

func (h *handler) ListStacks(ctx context.Context) (*generated.StacksResponse, error) {
	state, err := h.stateStore.Read(ctx)
	if err != nil {
		return nil, err
	}
	syncInfo, err := h.gitOpsStatus(ctx, state)
	if err != nil {
		return nil, err
	}

	return &generated.StacksResponse{
		Stacks: h.listStacks(state),
		Sync:   syncInfo,
	}, nil
}

func (h *handler) gitOpsStatus(ctx context.Context, state model.Runtime) (generated.SyncInfo, error) {
	info := generated.SyncInfo{}
	statusConfig := h.stackProvider.GitOpsStatusConfig()
	if statusConfig.SyncMode == config.SyncModePull || statusConfig.SyncMode == config.SyncModeHybrid {
		poll := generated.LastPollStatus{}
		if !state.LastPollAt.IsZero() {
			poll.At = generated.NewOptDateTime(state.LastPollAt)
			poll.Result = generated.NewOptLastPollStatusResult(generated.LastPollStatusResult(state.LastPollResult))
			poll.Error = generated.NewOptString(strings.TrimSpace(state.LastPollError))
		}
		info.LastPoll = generated.NewOptLastPollStatus(poll)
	}
	if statusConfig.WebhookEnabled {
		webhook := generated.LastWebhookStatus{}
		entries, err := h.history.ReadRecent(ctx, history.QueryOptions{
			Types: []events.TypeName{events.TypeNameWebhookReceived}, Limit: 1,
		})
		if err != nil {
			return generated.SyncInfo{}, fmt.Errorf("read latest webhook event: %w", err)
		}
		if len(entries) == 1 {
			queued, parseErr := strconv.ParseBool(entries[0].Details["queued"])
			if parseErr != nil {
				return generated.SyncInfo{}, fmt.Errorf("read latest webhook queued value: %w", parseErr)
			}
			webhook.EventID = generated.NewOptString(entries[0].ID)
			webhook.At = generated.NewOptDateTime(entries[0].CreatedAt)
			webhook.Queued = generated.NewOptBool(queued)
		}
		info.LastWebhook = generated.NewOptLastWebhookStatus(webhook)
	}
	if revision := strings.TrimSpace(state.GitRevision); revision != "" {
		info.LastRevision = generated.NewOptString(revision)
	}
	return info, nil
}
