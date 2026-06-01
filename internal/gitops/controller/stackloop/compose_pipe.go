package stackloop

import (
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

type pipeline struct {
	pipeline []pipelineStep
}

type pipelineStep struct {
	name string
	run  func(file *compose.File, stackName string) (bool, error)
}

func newPipeline(steps []pipelineStep) *pipeline {
	return &pipeline{
		pipeline: steps,
	}
}

func (p *pipeline) Run(file *compose.File, stackName string) (bool, *pipelineError) {
	changed := false

	for _, step := range p.pipeline {
		hasChanges, err := step.run(file, stackName)
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
