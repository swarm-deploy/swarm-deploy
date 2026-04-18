package handlers

import (
	"math"
	"time"

	"github.com/artarts36/swarm-deploy/internal/controller"
	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/artarts36/swarm-deploy/internal/service"
	serviceType "github.com/artarts36/swarm-deploy/internal/service/stype"
	"github.com/artarts36/swarm-deploy/internal/service/webroute"
	swarminspector "github.com/artarts36/swarm-deploy/internal/swarm/inspector"
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

func toGeneratedServiceStatus(status swarminspector.ServiceStatus) *generated.ServiceStatusResponse {
	resp := &generated.ServiceStatusResponse{
		Stack:   status.Stack,
		Service: status.Service,
		Spec:    toGeneratedServiceSpec(status.Spec),
	}

	return resp
}

func toGeneratedServiceSpec(spec swarminspector.ServiceSpec) generated.ServiceSpecResponse {
	mapped := generated.ServiceSpecResponse{
		Image:             spec.Image,
		Mode:              spec.Mode,
		Replicas:          toInt64FromUint64(spec.Replicas),
		RequestedRAMBytes: spec.RequestedRAMBytes,
		RequestedCPUNano:  spec.RequestedCPUNano,
		LimitRAMBytes:     spec.LimitRAMBytes,
		LimitCPUNano:      spec.LimitCPUNano,
		Secrets:           toGeneratedServiceSpecSecrets(spec.Secrets),
		Network:           toGeneratedServiceSpecNetworks(spec.Network),
	}

	if len(spec.Labels) > 0 {
		labels := make(generated.ServiceSpecResponseLabels, len(spec.Labels))
		for key, value := range spec.Labels {
			labels[key] = value
		}
		mapped.Labels = generated.NewOptServiceSpecResponseLabels(labels)
	}

	return mapped
}

func toGeneratedServiceSpecSecrets(secrets []swarminspector.ServiceSecret) []generated.ServiceSpecSecretResponse {
	if len(secrets) == 0 {
		return nil
	}

	mapped := make([]generated.ServiceSpecSecretResponse, 0, len(secrets))
	for _, secret := range secrets {
		item := generated.ServiceSpecSecretResponse{
			SecretName: secret.SecretName,
		}
		if secret.SecretID != "" {
			item.SecretID = generated.NewOptString(secret.SecretID)
		}
		if secret.Target != "" {
			item.Target = generated.NewOptString(secret.Target)
		}
		mapped = append(mapped, item)
	}

	return mapped
}

func toGeneratedServiceSpecNetworks(networks []swarminspector.ServiceNetwork) []generated.ServiceSpecNetworkResponse {
	if len(networks) == 0 {
		return nil
	}

	mapped := make([]generated.ServiceSpecNetworkResponse, 0, len(networks))
	for _, network := range networks {
		mapped = append(mapped, generated.ServiceSpecNetworkResponse{
			Target:  network.Target,
			Aliases: cloneStringSlice(network.Aliases),
		})
	}

	return mapped
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
			Name:          serviceInfo.Name,
			Stack:         serviceInfo.Stack,
			Type:          toGeneratedServiceType(serviceInfo.Type),
			Image:         serviceInfo.Image,
			RepositoryURL: toOptString(serviceInfo.RepositoryURL),
			Description:   toOptString(serviceInfo.Description),
			WebRoutes:     toGeneratedWebRoutes(serviceInfo.WebRoutes),
		}

		mapped = append(mapped, mappedItem)
	}
	return mapped
}

func toGeneratedWebRoutes(routes []webroute.Route) []generated.WebRoute {
	if len(routes) == 0 {
		return nil
	}

	mapped := make([]generated.WebRoute, 0, len(routes))
	for _, route := range routes {
		mapped = append(mapped, generated.WebRoute{
			Domain:  route.Domain,
			Address: route.Address,
			Port:    route.Port,
		})
	}

	return mapped
}

func toGeneratedNodes(nodes []swarminspector.NodeInfo) []generated.NodeInfo {
	mapped := make([]generated.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		mapped = append(mapped, generated.NodeInfo{
			ID:            node.ID,
			Hostname:      node.Hostname,
			Status:        node.Status,
			Availability:  node.Availability,
			ManagerStatus: node.ManagerStatus,
			EngineVersion: node.EngineVersion,
			Addr:          node.Addr,
		})
	}

	return mapped
}

func toGeneratedServiceType(typ serviceType.Type) generated.ServiceInfoType {
	switch typ {
	case serviceType.Application:
		return generated.ServiceInfoTypeApplication
	case serviceType.Monitoring:
		return generated.ServiceInfoTypeMonitoring
	case serviceType.Delivery:
		return generated.ServiceInfoTypeDelivery
	case serviceType.ReverseProxy:
		return generated.ServiceInfoTypeReverseProxy
	case serviceType.Database:
		return generated.ServiceInfoTypeDatabase
	default:
		return generated.ServiceInfoTypeApplication
	}
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, len(values))
	copy(out, values)

	return out
}

func toInt64FromUint64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}

	return int64(value)
}
