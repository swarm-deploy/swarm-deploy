package events

// GitFailureCategory identifies a stable class of Git remote failure.
type GitFailureCategory string

const (
	GitFailureAuthentication      GitFailureCategory = "authentication"
	GitFailureAccessDenied        GitFailureCategory = "access_denied"
	GitFailureRepositoryNotFound  GitFailureCategory = "repository_not_found"
	GitFailureNetwork             GitFailureCategory = "network"
	GitFailureSSHHostVerification GitFailureCategory = "ssh_host_verification"
	GitFailureOther               GitFailureCategory = "other"
)

// GitPullFailed is an internal durable observation of a failed remote pull.
type GitPullFailed struct {
	// Repository is the credential-free repository identifier.
	Repository string
	// Category is the stable failure classification.
	Category GitFailureCategory
	// Description is a sanitized diagnostic safe for persistence and notification.
	Description string
	// TriggerReason identifies the source of the pull attempt, when known.
	TriggerReason string
}

func (*GitPullFailed) Type() Type                 { return TypeGitPullFailed }
func (*GitPullFailed) Message() string            { return "Git pull failed" }
func (*GitPullFailed) Details() map[string]string { return nil }

// GitPullSucceeded is an internal durable recovery transition after a failed pull.
type GitPullSucceeded struct {
	// Repository is the credential-free repository identifier.
	Repository string
	// TriggerReason identifies the source of the successful pull attempt, when known.
	TriggerReason string
}

func (*GitPullSucceeded) Type() Type                 { return TypeGitPullSucceeded }
func (*GitPullSucceeded) Message() string            { return "Git repository access recovered" }
func (*GitPullSucceeded) Details() map[string]string { return nil }
