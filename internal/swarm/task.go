package swarm

// TaskState represents the state of a task.
type TaskState string

const (
	// TaskStateNew indicates that the task was newly created.
	TaskStateNew TaskState = "new"
	// TaskStateAllocated indicates that resources were allocated to the task.
	TaskStateAllocated TaskState = "allocated"
	// TaskStatePending indicates that the task is waiting for assignment.
	TaskStatePending TaskState = "pending"
	// TaskStateAssigned indicates that the task was assigned to a node.
	TaskStateAssigned TaskState = "assigned"
	// TaskStateAccepted indicates that the assigned node accepted the task.
	TaskStateAccepted TaskState = "accepted"
	// TaskStatePreparing indicates that the node is preparing the task.
	TaskStatePreparing TaskState = "preparing"
	// TaskStateReady indicates that the task is ready to start.
	TaskStateReady TaskState = "ready"
	// TaskStateStarting indicates that the task is starting.
	TaskStateStarting TaskState = "starting"
	// TaskStateRunning indicates that the task is running.
	TaskStateRunning TaskState = "running"
	// TaskStateComplete indicates that the task completed successfully.
	TaskStateComplete TaskState = "complete"
	// TaskStateShutdown indicates that the task was shut down.
	TaskStateShutdown TaskState = "shutdown"
	// TaskStateFailed indicates that the task failed.
	TaskStateFailed TaskState = "failed"
	// TaskStateRejected indicates that the task was rejected.
	TaskStateRejected TaskState = "rejected"
	// TaskStateRemove indicates that the task is being removed.
	TaskStateRemove TaskState = "remove"
	// TaskStateOrphaned indicates that the task is orphaned.
	TaskStateOrphaned TaskState = "orphaned"
)
