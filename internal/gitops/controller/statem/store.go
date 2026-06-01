package statem

type Store interface {
	ReadStore

	// Update applies mutation to runtime state.
	Update(fn func(*Runtime))

	Stop()
}

type ReadStore interface {
	// Get returns a snapshot copy of current runtime state.
	Get() Runtime
}
