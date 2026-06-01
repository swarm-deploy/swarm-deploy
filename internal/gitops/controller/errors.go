package controller

import (
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

type stackReconcileError struct {
	op       string
	services []compose.Service
	err      error
}

type pipelineError struct {
	stepName string
	err      error
}

func (e *pipelineError) Error() string {
	return fmt.Sprintf("%s: %s", e.stepName, e.err.Error())
}

func (e *stackReconcileError) Error() string {
	return fmt.Sprintf("%s: %v", e.op, e.err)
}

func (e *stackReconcileError) Unwrap() error {
	return e.err
}

func (e *stackReconcileError) FailedServices() []compose.Service {
	return e.services
}

func wrapStackReconcileError(op string, services []compose.Service, err error) error {
	if err == nil {
		return nil
	}

	return &stackReconcileError{
		op:       op,
		services: services,
		err:      err,
	}
}

func failedServicesFromReconcileError(err error) []compose.Service {
	var reconcileErr *stackReconcileError
	// Preserve detailed service context when the caller receives wrapped errors.
	if errors.As(err, &reconcileErr) {
		return reconcileErr.FailedServices()
	}
	return nil
}
