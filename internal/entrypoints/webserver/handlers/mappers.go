package handlers

import (
	"time"

	"github.com/artarts36/swarm-deploy/internal/controller"
	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

func toGeneratedStacks(stacks []controller.StackView) []generated.StackView {
	mapped := make([]generated.StackView, 0, len(stacks))

	for _, stack := range stacks {
		mapped = append(mapped, toGeneratedStack(stack))
	}

	return mapped
}

func toGeneratedStack(stack controller.StackView) generated.StackView {
	mapped := generated.StackView{
		Name:        stack.Name,
		ComposeFile: stack.ComposeFile,
		LastStatus:  stack.LastStatus,
		Services:    toGeneratedServices(stack.Services),
		LastError:   toOptString(stack.LastError),
		LastCommit:  toOptString(stack.LastCommit),
		LastDeployAt: toOptDateTime(
			stack.LastDeployAt,
		),
		SourceDigest: toOptString(stack.SourceDigest),
	}

	return mapped
}

func toGeneratedServices(services []controller.ServiceView) []generated.ServiceView {
	mapped := make([]generated.ServiceView, 0, len(services))

	for _, service := range services {
		mappedService := generated.ServiceView{
			Name:         service.Name,
			Image:        service.Image,
			ImageVersion: service.ImageVersion,
			LastStatus:   toOptString(service.LastStatus),
			LastDeployAt: toOptDateTime(service.LastDeployAt),
		}

		mapped = append(mapped, mappedService)
	}

	return mapped
}

func toOptString(value string) generated.OptString {
	if value == "" {
		return generated.OptString{}
	}

	return generated.NewOptString(value)
}

func toOptDateTime(value time.Time) generated.OptDateTime {
	if value.IsZero() {
		return generated.OptDateTime{}
	}

	return generated.NewOptDateTime(value)
}

func toGeneratedServiceStatus(status swarm.ServiceStatus) *generated.ServiceStatusResponse {
	resp := &generated.ServiceStatusResponse{
		Stack:             status.Stack,
		Service:           status.Service,
		Image:             status.Image,
		RequestedRAMBytes: status.RequestedRAMBytes,
		RequestedCPUNano:  status.RequestedCPUNano,
		LimitRAMBytes:     status.LimitRAMBytes,
		LimitCPUNano:      status.LimitCPUNano,
	}

	return resp
}

func toGeneratedEvents(entries []history.Entry) []generated.EventHistoryItem {
	mapped := make([]generated.EventHistoryItem, 0, len(entries))
	for _, entry := range entries {
		item := generated.EventHistoryItem{
			Type:      string(entry.Type),
			CreatedAt: entry.CreatedAt,
			Message:   entry.Message,
		}
		if len(entry.Details) > 0 {
			details := make(map[string]string, len(entry.Details))
			for key, value := range entry.Details {
				details[key] = value
			}
			item.Details = generated.NewOptEventHistoryItemDetails(details)
		}
		mapped = append(mapped, item)
	}

	return mapped
}

func toGeneratedServiceInfos(services []service.Info) []generated.ServiceInfo {
	mapped := make([]generated.ServiceInfo, 0, len(services))
	for _, serviceInfo := range services {
		mappedItem := generated.ServiceInfo{
			Name:        serviceInfo.Name,
			Stack:       serviceInfo.Stack,
			Type:        toGeneratedServiceType(serviceInfo.Type),
			Image:       serviceInfo.Image,
			Description: toOptString(serviceInfo.Description),
		}

		mapped = append(mapped, mappedItem)
	}
	return mapped
}

func toGeneratedServiceType(serviceType service.Type) generated.ServiceInfoType {
	switch serviceType {
	case service.TypeApplication:
		return generated.ServiceInfoTypeApplication
	case service.TypeMonitoring:
		return generated.ServiceInfoTypeMonitoring
	case service.TypeDelivery:
		return generated.ServiceInfoTypeDelivery
	case service.TypeReverseProxy:
		return generated.ServiceInfoTypeReverseProxy
	case service.TypeDatabase:
		return generated.ServiceInfoTypeDatabase
	default:
		return generated.ServiceInfoTypeApplication
	}
}
