package metadata

// Labels groups metadata labels from different inspection scopes.
type Labels struct {
	// Service contains labels from docker service annotations.
	Service map[string]string
	// Container contains labels from service task container spec.
	Container map[string]string
	// Image contains labels from OCI image config.
	Image map[string]string
}

type LabelResolver interface {
	Resolve(labels Labels, meta *Metadata)
}
