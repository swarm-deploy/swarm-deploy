package labelsdict

const (
	StackNamespace = "com.docker.stack.namespace"
)

func GetStackName(labels map[string]string) string {
	return labels[StackNamespace]
}
