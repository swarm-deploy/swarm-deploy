package labelsdict

import "strings"

const (
	NetworkManagedKey   = "org.swarm-deploy.network.managed"
	NetworkManagedValue = "true"
)

func NetworkManaged(labels map[string]string) bool {
	return strings.EqualFold(labels[NetworkManagedKey], NetworkManagedValue)
}
