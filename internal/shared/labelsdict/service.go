package labelsdict

import "strings"

const (
	ServiceManagedLabelKey   = "org.swarm-deploy.service.managed"
	ServiceManagedLabelValue = "true"

	ServiceSyncPolicyPruneLabelKey = "org.swarm-deploy.service.sync.policy.prune"

	ServiceType = "org.swarm-deploy.service.type"
)

func ServiceManaged(labels map[string]string) bool {
	return strings.EqualFold(labels[ServiceManagedLabelKey], ServiceManagedLabelValue)
}
