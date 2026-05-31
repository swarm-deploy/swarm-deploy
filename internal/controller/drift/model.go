package drift

type Response struct {
	Drifts []*Drift
}

// Drift describes divergence between desired and live service state.
type Drift struct {
	ServiceName string

	// OutOfSync is true when at least one drift condition is detected.
	OutOfSync bool
	// ServiceMissed is true when service is not found in cluster runtime.
	ServiceMissed bool

	Env struct {
		OutOfSync bool

		Missed    []string
		Redundant []string
	}
}
