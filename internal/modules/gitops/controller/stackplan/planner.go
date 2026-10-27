// Package stackplan decides which stacks a sync run deploys and in what order.
package stackplan

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
)

// Plan is the result of stack planning for one sync run.
type Plan struct {
	// Stacks are all configured stacks in deployment order:
	// stacks touched by the Git diff first (in diff order), then the rest in config order.
	Stacks []config.StackSpec
	// Changed holds names of stacks touched by the Git diff. Nil when nothing changed
	// or the diff was not computed.
	Changed map[string]bool
	// DiffErr is a non-fatal diff failure. Stacks keep the configured order in that case.
	DiffErr error
}

// Planner builds a Plan from configured stacks and a Git pull result.
type Planner struct {
	diff DiffSource
}

// NewPlanner creates a Planner.
func NewPlanner(diff DiffSource) *Planner {
	return &Planner{diff: diff}
}

// Plan orders stacks so that stacks changed between git.OldRevision and git.NewRevision go first.
// When the revision was not updated, stacks are returned in configured order without computing a diff.
func (p *Planner) Plan(ctx context.Context, stacks []config.StackSpec, git gitx.PullResult) Plan {
	plan := Plan{Stacks: stacks}
	if !git.Updated || len(stacks) == 0 {
		return plan
	}

	fileDiffs, err := p.diff.Diff(ctx, git.OldRevision, git.NewRevision)
	if err != nil {
		plan.DiffErr = err
		return plan
	}

	order := changeOrder(stacks, fileDiffs)
	if len(order) == 0 {
		return plan
	}

	plan.Stacks = sortByChange(stacks, order)
	plan.Changed = make(map[string]bool, len(order))
	for name := range order {
		plan.Changed[name] = true
	}

	return plan
}

// changeOrder maps a stack name to the index of the first diff entry touching the stack.
func changeOrder(stacks []config.StackSpec, fileDiffs []gitx.CommitFileDiff) map[string]int {
	stacksByComposePath := make(map[string][]string, len(stacks))
	for _, stack := range stacks {
		composePath := normalizePath(stack.ComposeFile)
		if composePath == "" {
			continue
		}
		stacksByComposePath[composePath] = append(stacksByComposePath[composePath], stack.Name)
	}

	order := make(map[string]int, len(stacks))
	for diffIndex, fileDiff := range fileDiffs {
		changedPath := normalizePath(fileDiff.NewPath)
		if changedPath == "" {
			changedPath = normalizePath(fileDiff.OldPath)
		}
		if changedPath == "" {
			continue
		}

		for _, stackName := range stacksByComposePath[changedPath] {
			if _, seen := order[stackName]; !seen {
				order[stackName] = diffIndex
			}
		}
	}

	return order
}

func sortByChange(stacks []config.StackSpec, order map[string]int) []config.StackSpec {
	ordered := slices.Clone(stacks)
	slices.SortStableFunc(ordered, func(left, right config.StackSpec) int {
		leftOrder, leftChanged := order[left.Name]
		rightOrder, rightChanged := order[right.Name]

		switch {
		case leftChanged && rightChanged:
			return cmp.Compare(leftOrder, rightOrder)
		case leftChanged:
			return -1
		case rightChanged:
			return 1
		default:
			return 0
		}
	})

	return ordered
}

func normalizePath(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(path), "./")
}
