package compose

// IsSwarmIgnoredOption identifies explicitly tolerated service options that Swarm ignores.
func IsSwarmIgnoredOption(option string) bool {
	return option == "privileged" || option == "depends_on" || option == "restart"
}
