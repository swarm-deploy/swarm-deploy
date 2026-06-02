package pipe

import "fmt"

type Error struct {
	StepName string
	Err      error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.StepName, e.Err.Error())
}

func (e *Error) Unwrap() error {
	return e.Err
}
