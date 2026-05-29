package deployer

import "fmt"

type JobFailedError struct {
	// ID is a failed Docker task identifier.
	ID string
	// Name is an init job service name.
	Name string
	// Reason is a failure reason from task status.
	Reason string
	logs   []string
}

func (e *JobFailedError) Error() string {
	return fmt.Sprintf("job %q with id %q failed: %s", e.Name, e.ID, e.Reason)
}

func (e *JobFailedError) Logs() []string {
	return e.logs
}
