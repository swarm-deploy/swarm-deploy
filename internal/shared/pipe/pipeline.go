package pipe

import (
	"context"
)

type Pipeline[pt any] struct {
	pipeline []Step[pt]
}

func NewPipeline[pt any]() *Pipeline[pt] {
	return &Pipeline[pt]{
		pipeline: make([]Step[pt], 0),
	}
}

func (p *Pipeline[pt]) Add(step Step[pt]) {
	if step.When == nil {
		step.When = always[pt]()
	}

	p.pipeline = append(p.pipeline, step)
}

func (p *Pipeline[pt]) Run(ctx context.Context, payload pt) *Error {
	for _, step := range p.pipeline {
		if !step.When(payload) {
			continue
		}

		err := step.Run(ctx, payload)
		if err != nil {
			return &Error{
				StepName: step.Name,
				Err:      err,
			}
		}
	}

	return nil
}
