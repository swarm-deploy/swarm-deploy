package labelsdict

const (
	StackNamespace = "com.docker.stack.namespace"

	// RotatedResourceManagedLabelKey marks configs and secrets created by the rotation mechanism.
	RotatedResourceManagedLabelKey = "org.swarm-deploy.rotation.managed"
	// RotatedResourceManagedLabelValue is the managed rotation label value.
	RotatedResourceManagedLabelValue = "true"
	// RotatedResourceLogicalNameLabelKey stores the compose-level config or secret key.
	RotatedResourceLogicalNameLabelKey = "org.swarm-deploy.rotation.logical-name"
)

func GetStackName(labels map[string]string) string {
	return labels[StackNamespace]
}
