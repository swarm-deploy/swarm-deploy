package model

import "github.com/swarm-deploy/swarm-deploy/internal/compose"

// Stack is a stack definition prepared for recommendation analysis.
type Stack struct {
	// Name is a stack name.
	Name string
	// Definition is a stack compose definition.
	Definition compose.File

	Commit string
}
