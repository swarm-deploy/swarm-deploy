package secretmanager

import "time"

const cloudSecretsKind = "cloud-secrets"

// Provider describes the external secret provider used by a Secret Manager.
type Provider struct {
	// Name is the provider display name.
	Name string
	// Links contains provider documentation and management destinations.
	Links ProviderLinks
}

// ProviderLinks contains optional links exposed by a Secret Manager provider.
type ProviderLinks struct {
	// Doc points to the provider documentation.
	Doc string
	// Manager points to the provider management UI.
	Manager string
}

// Info describes a discovered Secret Manager and its controller status.
type Info struct {
	// Stack is the Docker stack containing the Secret Manager.
	Stack string
	// Service is the service name inside the stack.
	Service string
	// Kind identifies the Secret Manager implementation.
	Kind string
	// Controllable reports whether a controller endpoint was discovered.
	Controllable bool
	// Available reports whether the controller answered the latest probe.
	Available bool
	// Version is the Secret Manager version reported by its controller.
	Version string
	// Provider describes the configured external provider.
	Provider Provider
	// LastSyncAt is the last successful synchronization known by the controller.
	LastSyncAt *time.Time
	// NextSyncAt is the next synchronization scheduled by the controller.
	NextSyncAt *time.Time
	// Error contains a controller discovery or availability error.
	Error string
}

// SyncResult summarizes a manually triggered Secret Manager synchronization.
type SyncResult struct {
	// Created is the number of newly created logical secrets.
	Created uint32
	// Updated is the number of updated logical secrets.
	Updated uint32
	// Removed is the number of removed logical secrets.
	Removed uint32
	// Unchanged is the number of unchanged logical secrets.
	Unchanged uint32
}
