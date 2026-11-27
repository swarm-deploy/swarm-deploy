package conversation

import "time"

// Turn describes one message in a conversation.
type Turn struct {
	// Sequence is assigned by the history repository and omitted from the stored payload.
	Sequence int `json:"-"`
	// Role is a participant role ("user", "assistant", or "system").
	Role string `json:"role"`
	// Content is a raw message text.
	Content string `json:"content"`
	// Activity contains safe execution trace metadata for assistant turns.
	Activity []string `json:"activity,omitempty"`
	// Selection contains structured reply options rendered by clients.
	Selection *Selection `json:"selection,omitempty"`
	// Artifacts contains structured outputs produced by an assistant operation.
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// ArtifactType identifies the presentation kind of an assistant artifact.
type ArtifactType string

const (
	// ArtifactTypePullRequest identifies a published Git pull request.
	ArtifactTypePullRequest ArtifactType = "pull_request"
	// ArtifactTypeGitBranch identifies a published Git branch.
	ArtifactTypeGitBranch ArtifactType = "git_branch"
)

// Artifact describes a durable output produced by an assistant operation.
type Artifact struct {
	// Type identifies how clients present the artifact.
	Type ArtifactType `json:"type"`
	// Label is the user-visible artifact name.
	Label string `json:"label"`
	// URL opens the artifact in its external system when available.
	URL string `json:"url,omitempty"`
}

// Selection contains a single-choice prompt attached to an assistant turn.
type Selection struct {
	// Options contains the values available for selection.
	Options []SelectionOption `json:"options"`
}

// SelectionOption describes one value that can be sent as the next user message.
type SelectionOption struct {
	// Label is the primary user-visible option text.
	Label string `json:"label"`
	// Description is optional secondary context for the option.
	Description string `json:"description,omitempty"`
	// Value is the exact message submitted when the option is selected.
	Value string `json:"value"`
}

// Conversation contains conversation messages and metadata.
type Conversation struct {
	// ReadOnly indicates that writable tools are disabled for this conversation.
	ReadOnly bool
	// Turns keeps conversation messages in chronological order.
	Turns []Turn
	// LastMessageAt is a timestamp of the latest appended message.
	LastMessageAt time.Time
}

// Storage persists conversations.
type Storage interface {
	// Get returns conversation by conversation id.
	Get(id string) (Conversation, bool)
	// Append appends turns to conversation and updates LastMessageAt.
	Append(id string, readOnly bool, turns ...Turn)
}
