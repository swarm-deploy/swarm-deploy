package stackloop

import (
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

type reconcileError struct {
	op       string
	services []compose.Service
	err      error
}

func (e *reconcileError) Error() string {
	return fmt.Sprintf("%s: %s", e.op, faults.SafeDiagnostic(e.err))
}

func (e *reconcileError) Unwrap() error {
	return e.err
}

func (e *reconcileError) FailedServices() []compose.Service {
	return e.services
}

func wrapReconcileError(op string, services []compose.Service, err error) error {
	if err == nil {
		return nil
	}

	return &reconcileError{
		op:       op,
		services: services,
		err:      err,
	}
}

// FailedServicesFromError extracts service context from reconcile failures.
func FailedServicesFromError(err error) []compose.Service {
	var reconcileErr *reconcileError
	// Preserve detailed service context when the caller receives wrapped errors.
	if errors.As(err, &reconcileErr) {
		return reconcileErr.FailedServices()
	}
	return nil
}
