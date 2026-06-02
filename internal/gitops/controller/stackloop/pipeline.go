package stackloop

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

type pipelinePayload struct {
	Stack config.StackSpec

	Desired        *compose.File
	DesiredMutated bool
}

type pipeline struct {
	pipeline []pipelineStep
}

type pipelineStep struct {
	name string
	when func(payload *pipelinePayload) bool
	run  func(payload *pipelinePayload) (bool, error)
}

func newPipeline() *pipeline {
	return &pipeline{
		pipeline: make([]pipelineStep, 0),
	}
}

func (p *pipeline) Add(step pipelineStep) {
	if step.when == nil {
		step.when = func(*pipelinePayload) bool {
			return true
		}
	}

	p.pipeline = append(p.pipeline, step)
}

func (p *pipeline) Run(payload *pipelinePayload) (bool, *pipelineError) {
	changed := false

	for _, step := range p.pipeline {
		if !step.when(payload) {
			continue
		}

		hasChanges, err := step.run(payload)
		if err != nil {
			return false, &pipelineError{
				stepName: step.name,
				err:      err,
			}
		}

		if hasChanges {
			changed = true
		}
	}

	return changed, nil
}
