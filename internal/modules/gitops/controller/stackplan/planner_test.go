package stackplan

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
)

type fakeDiffSource struct {
	diffs []gitx.CommitFileDiff
	err   error
	calls int
}

func (f *fakeDiffSource) Diff(context.Context, string, string) ([]gitx.CommitFileDiff, error) {
	f.calls++
	return f.diffs, f.err
}

func stacks(specs ...string) []config.StackSpec {
	// specs is a flat list of name, compose file pairs.
	result := make([]config.StackSpec, 0, len(specs)/2)
	for i := 0; i+1 < len(specs); i += 2 {
		result = append(result, config.StackSpec{Name: specs[i], ComposeFile: specs[i+1]})
	}
	return result
}

func names(stacks []config.StackSpec) []string {
	result := make([]string, 0, len(stacks))
	for _, stack := range stacks {
		result = append(result, stack.Name)
	}
	return result
}

var updated = gitx.PullResult{OldRevision: "commit-1", NewRevision: "commit-2", Updated: true}

func TestPlannerPlan(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stacks      []config.StackSpec
		diffs       []gitx.CommitFileDiff
		wantOrder   []string
		wantChanged []string
	}{
		{
			name:      "no matching diff keeps configured order",
			stacks:    stacks("a", "a.yaml", "b", "b.yaml"),
			diffs:     []gitx.CommitFileDiff{{NewPath: "README.md"}},
			wantOrder: []string{"a", "b"},
		},
		{
			name:        "changed stack goes first",
			stacks:      stacks("a", "a.yaml", "b", "b.yaml", "c", "c.yaml"),
			diffs:       []gitx.CommitFileDiff{{NewPath: "c.yaml"}},
			wantOrder:   []string{"c", "a", "b"},
			wantChanged: []string{"c"},
		},
		{
			name:        "changed stacks follow diff order",
			stacks:      stacks("a", "a.yaml", "b", "b.yaml", "c", "c.yaml"),
			diffs:       []gitx.CommitFileDiff{{NewPath: "c.yaml"}, {NewPath: "a.yaml"}},
			wantOrder:   []string{"c", "a", "b"},
			wantChanged: []string{"a", "c"},
		},
		{
			name:   "first diff occurrence wins",
			stacks: stacks("a", "a.yaml", "b", "b.yaml"),
			diffs: []gitx.CommitFileDiff{
				{NewPath: "a.yaml"}, {NewPath: "b.yaml"}, {NewPath: "a.yaml"},
			},
			wantOrder:   []string{"a", "b"},
			wantChanged: []string{"a", "b"},
		},
		{
			name:        "stacks sharing a compose file are all prioritized",
			stacks:      stacks("a", "a.yaml", "b", "shared.yaml", "c", "shared.yaml"),
			diffs:       []gitx.CommitFileDiff{{NewPath: "shared.yaml"}},
			wantOrder:   []string{"b", "c", "a"},
			wantChanged: []string{"b", "c"},
		},
		{
			name:        "paths are normalized",
			stacks:      stacks("a", "a.yaml", "b", "./stacks/b.yaml"),
			diffs:       []gitx.CommitFileDiff{{NewPath: " stacks/b.yaml "}},
			wantOrder:   []string{"b", "a"},
			wantChanged: []string{"b"},
		},
		{
			name:        "deleted file falls back to old path",
			stacks:      stacks("a", "a.yaml", "b", "b.yaml"),
			diffs:       []gitx.CommitFileDiff{{OldPath: "b.yaml"}},
			wantOrder:   []string{"b", "a"},
			wantChanged: []string{"b"},
		},
		{
			name:      "stack without compose file is never matched",
			stacks:    stacks("a", "", "b", "b.yaml"),
			diffs:     []gitx.CommitFileDiff{{NewPath: ""}},
			wantOrder: []string{"a", "b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &fakeDiffSource{diffs: tc.diffs}

			plan := New(source).Plan(t.Context(), tc.stacks, updated)

			require.NoError(t, plan.DiffErr)
			assert.Equal(t, tc.wantOrder, names(plan.Stacks), "stack order")
			var changed []string
			for name := range plan.Changed {
				changed = append(changed, name)
			}
			assert.ElementsMatch(t, tc.wantChanged, changed, "changed stacks")
			assert.Equal(t, 1, source.calls, "diff calls")
		})
	}
}

func TestPlannerPlanSkipsDiffWhenNotUpdated(t *testing.T) {
	source := &fakeDiffSource{}
	configured := stacks("a", "a.yaml", "b", "b.yaml")

	plan := New(source).Plan(t.Context(), configured, gitx.PullResult{OldRevision: "c1", NewRevision: "c1"})

	assert.Equal(t, configured, plan.Stacks)
	assert.Nil(t, plan.Changed)
	assert.NoError(t, plan.DiffErr)
	assert.Zero(t, source.calls, "diff must not be computed")
}

func TestPlannerPlanKeepsOrderWhenDiffFails(t *testing.T) {
	diffErr := errors.New("diff failed")
	configured := stacks("a", "a.yaml", "b", "b.yaml")

	plan := New(&fakeDiffSource{err: diffErr}).Plan(t.Context(), configured, updated)

	assert.Equal(t, configured, plan.Stacks)
	assert.Nil(t, plan.Changed)
	assert.ErrorIs(t, plan.DiffErr, diffErr)
}

func TestPlannerPlanDoesNotMutateInput(t *testing.T) {
	configured := stacks("a", "a.yaml", "b", "b.yaml")
	snapshot := append([]config.StackSpec(nil), configured...)

	plan := New(&fakeDiffSource{diffs: []gitx.CommitFileDiff{{NewPath: "b.yaml"}}}).
		Plan(t.Context(), configured, updated)

	assert.Equal(t, []string{"b", "a"}, names(plan.Stacks))
	assert.Equal(t, snapshot, configured, "input slice must stay in configured order")
}
