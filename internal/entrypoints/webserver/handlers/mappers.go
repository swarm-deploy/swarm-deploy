package handlers

import (
	"math"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/controller"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/imageref"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/webroute"
)

const (
	externalPathLabel      = "external_path"
	externalVersionIDLabel = "external_version_id"
	dockerLabelPrefix      = "com.docker."
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
		LastError:   toOptString(stack.LastError),
		LastCommit:  toOptString(stack.LastCommit),
		LastDeployAt: toOptDateTime(
			stack.LastDeployAt,
		),
		SourceDigest: toOptString(stack.SourceDigest),
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
		Stack:   status.Stack,
		Service: status.Service,
		Spec:    toGeneratedServiceSpec(status.Spec),
	}

	return resp
}

func toGeneratedServiceDeployments(
	entries []history.Entry,
	stackName string,
	serviceName string,
	image string,
	limit int,
) []generated.ServiceDeploymentResponse {
	if len(entries) == 0 || stackName == "" {
		return []generated.ServiceDeploymentResponse{}
	}

	imageVersion := imageref.Version(image)
	out := make([]generated.ServiceDeploymentResponse, 0, len(entries))

	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]

		status, ok := toGeneratedServiceDeploymentStatus(entry.Type)
		if !ok {
			continue
		}

		if entry.Details["stack"] != stackName {
			continue
		}
		if serviceInEvent := entry.Details["service"]; serviceInEvent != "" && serviceInEvent != serviceName {
			continue
		}

		item := generated.ServiceDeploymentResponse{
			CreatedAt:    entry.CreatedAt,
			Status:       status,
			Image:        image,
			ImageVersion: imageVersion,
		}

		if entry.Message != "" {
			item.Message = generated.NewOptString(entry.Message)
		}
		if commit := entry.Details["commit"]; commit != "" {
			item.Commit = generated.NewOptString(commit)
		}

		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}

	return out
}

func toGeneratedServiceDeploymentStatus(typ events.Type) (generated.ServiceDeploymentStatus, bool) {
	switch typ {
	case events.TypeDeploySuccess:
		return generated.ServiceDeploymentStatusSuccess, true
	case events.TypeDeployFailed:
		return generated.ServiceDeploymentStatusFailed, true
	default:
		return "", false
	}
}

func toGeneratedServiceSpec(spec swarm.ServiceSpec) generated.ServiceSpecResponse {
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
		dockerLabels := make(generated.ServiceSpecLabelGroupResponse)
		customLabels := make(generated.ServiceSpecLabelGroupResponse)

		for key, value := range spec.Labels {
			if strings.HasPrefix(key, dockerLabelPrefix) {
				dockerLabels[key] = value
				continue
			}

			customLabels[key] = value
		}

		groupedLabels := generated.ServiceSpecLabelsResponse{}
		if len(dockerLabels) > 0 {
			groupedLabels.Docker = generated.NewOptServiceSpecLabelGroupResponse(dockerLabels)
		}
		if len(customLabels) > 0 {
			groupedLabels.Custom = generated.NewOptServiceSpecLabelGroupResponse(customLabels)
		}
		if groupedLabels.Docker.IsSet() || groupedLabels.Custom.IsSet() {
			mapped.Labels = generated.NewOptServiceSpecLabelsResponse(groupedLabels)
		}
	}

	return mapped
}

func toGeneratedServiceSpecSecrets(secrets []swarm.ServiceSecret) []generated.ServiceSpecSecretResponse {
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

func toGeneratedServiceSpecNetworks(networks []swarm.ServiceNetwork) []generated.ServiceSpecNetworkResponse {
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
			Type:      entry.Type.String(),
			Severity:  toGeneratedEventSeverity(entry.Severity),
			Category:  toGeneratedEventCategory(entry.Category),
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

func toGeneratedEventSeverity(severity events.Severity) generated.EventSeverity {
	switch severity {
	case events.SeverityWarn:
		return generated.EventSeverityWarn
	case events.SeverityError:
		return generated.EventSeverityError
	case events.SeverityAlert:
		return generated.EventSeverityAlert
	case events.SeverityInfo:
		fallthrough
	default:
		return generated.EventSeverityInfo
	}
}

func toGeneratedEventCategory(category events.Category) generated.EventCategory {
	switch category {
	case events.CategorySecurity:
		return generated.EventCategorySecurity
	case events.CategorySync:
		fallthrough
	default:
		return generated.EventCategorySync
	}
}

func toGeneratedServiceInfos(services []service.Info) []generated.ServiceInfo {
	mapped := make([]generated.ServiceInfo, 0, len(services))
	for _, serviceInfo := range services {
		mapped = append(mapped, toGeneratedServiceInfo(serviceInfo))
	}
	return mapped
}

func toGeneratedServiceInfo(serviceInfo service.Info) generated.ServiceInfo {
	return generated.ServiceInfo{
		Name:          serviceInfo.Name,
		Stack:         serviceInfo.Stack,
		Type:          toGeneratedServiceType(serviceInfo.Type),
		TypeTitle:     serviceType.Title(serviceInfo.Type),
		Image:         serviceInfo.Image,
		ImageVersion:  imageref.Version(serviceInfo.Image),
		RepositoryURL: toOptString(serviceInfo.RepositoryURL),
		Description:   toOptString(serviceInfo.Description),
		WebRoutes:     toGeneratedWebRoutes(serviceInfo.WebRoutes),
	}
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

func toGeneratedNodes(nodes []swarm.Node) []generated.NodeInfo {
	mapped := make([]generated.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		mapped = append(mapped, generated.NodeInfo{
			ID:            node.ID,
			Hostname:      node.Hostname,
			Status:        node.Status,
			Availability:  node.Availability,
			ManagerStatus: string(node.ManagerStatus),
			EngineVersion: node.EngineVersion,
			Addr:          node.Addr,
		})
	}

	return mapped
}

func toGeneratedNetworks(networks []swarm.Network) []generated.NetworkInfo {
	mapped := make([]generated.NetworkInfo, 0, len(networks))
	for _, network := range networks {
		item := generated.NetworkInfo{
			ID:         network.ID,
			Name:       network.Name,
			Scope:      network.Scope,
			Driver:     network.Driver,
			Internal:   network.Internal,
			Attachable: network.Attachable,
			Ingress:    network.Ingress,
		}
		if len(network.Labels) > 0 {
			item.Labels = generated.NewOptNetworkInfoLabels(cloneStringMap(network.Labels))
		}
		if len(network.Options) > 0 {
			item.Options = generated.NewOptNetworkInfoOptions(cloneStringMap(network.Options))
		}

		mapped = append(mapped, item)
	}

	return mapped
}

func toGeneratedSecrets(secrets []swarm.Secret) []generated.SecretInfo {
	mapped := make([]generated.SecretInfo, 0, len(secrets))
	for _, secret := range secrets {
		item := generated.SecretInfo{
			ID:        secret.ID,
			Name:      secret.Name,
			VersionID: toInt64FromUint64(secret.VersionID),
			CreatedAt: secret.CreatedAt,
			External:  toGeneratedSecretExternal(secret.Labels),
		}

		mapped = append(mapped, item)
	}

	return mapped
}

func toGeneratedSecretExternal(labels map[string]string) generated.OptSecretExternalInfo {
	if len(labels) == 0 {
		return generated.OptSecretExternalInfo{}
	}

	external := generated.SecretExternalInfo{}
	hasExternalData := false

	if path, ok := labels[externalPathLabel]; ok && path != "" {
		external.Path = generated.NewOptString(path)
		hasExternalData = true
	}

	if versionID, ok := labels[externalVersionIDLabel]; ok && versionID != "" {
		external.VersionID = generated.NewOptString(versionID)
		hasExternalData = true
	}

	if !hasExternalData {
		return generated.OptSecretExternalInfo{}
	}

	return generated.NewOptSecretExternalInfo(external)
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

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}

	return out
}

func toInt64FromUint64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}

	return int64(value)
}
