package stackplan

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"go.uber.org/mock/gomock"
)

func TestPlannerPlan(t *testing.T) {
	updated := gitx.PullResult{OldRevision: "commit-1", NewRevision: "commit-2", Updated: true}
	errDiff := errors.New("diff failed")

	for _, tc := range []struct {
		name        string
		stacks      []config.StackSpec
		pull        gitx.PullResult
		expectDiff  bool
		diffs       []gitx.CommitFileDiff
		diffErr     error
		wantOrder   []string
		wantChanged []string
		wantDiffErr error
	}{
		{
			name:      "not updated revision skips diff",
			stacks:    []config.StackSpec{{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "b.yaml"}},
			pull:      gitx.PullResult{OldRevision: "commit-1", NewRevision: "commit-1"},
			wantOrder: []string{"a", "b"},
		},
		{
			name:      "no stacks skips diff",
			pull:      updated,
			wantOrder: []string{},
		},
		{
			name:        "diff failure keeps configured order",
			stacks:      []config.StackSpec{{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "b.yaml"}},
			pull:        updated,
			expectDiff:  true,
			diffErr:     errDiff,
			wantOrder:   []string{"a", "b"},
			wantDiffErr: errDiff,
		},
		{
			name:       "no matching diff keeps configured order",
			stacks:     []config.StackSpec{{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "b.yaml"}},
			pull:       updated,
			expectDiff: true,
			diffs:      []gitx.CommitFileDiff{{NewPath: "README.md"}},
			wantOrder:  []string{"a", "b"},
		},
		{
			name: "changed stack goes first",
			stacks: []config.StackSpec{
				{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "b.yaml"}, {Name: "c", ComposeFile: "c.yaml"},
			},
			pull:        updated,
			expectDiff:  true,
			diffs:       []gitx.CommitFileDiff{{NewPath: "c.yaml"}},
			wantOrder:   []string{"c", "a", "b"},
			wantChanged: []string{"c"},
		},
		{
			name: "changed stacks follow diff order",
			stacks: []config.StackSpec{
				{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "b.yaml"}, {Name: "c", ComposeFile: "c.yaml"},
			},
			pull:        updated,
			expectDiff:  true,
			diffs:       []gitx.CommitFileDiff{{NewPath: "c.yaml"}, {NewPath: "a.yaml"}},
			wantOrder:   []string{"c", "a", "b"},
			wantChanged: []string{"a", "c"},
		},
		{
			name:       "first diff occurrence wins",
			stacks:     []config.StackSpec{{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "b.yaml"}},
			pull:       updated,
			expectDiff: true,
			diffs: []gitx.CommitFileDiff{
				{NewPath: "a.yaml"}, {NewPath: "b.yaml"}, {NewPath: "a.yaml"},
			},
			wantOrder:   []string{"a", "b"},
			wantChanged: []string{"a", "b"},
		},
		{
			name: "stacks sharing a compose file are all prioritized",
			stacks: []config.StackSpec{
				{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "shared.yaml"}, {Name: "c", ComposeFile: "shared.yaml"},
			},
			pull:        updated,
			expectDiff:  true,
			diffs:       []gitx.CommitFileDiff{{NewPath: "shared.yaml"}},
			wantOrder:   []string{"b", "c", "a"},
			wantChanged: []string{"b", "c"},
		},
		{
			name:        "paths are normalized",
			stacks:      []config.StackSpec{{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "./stacks/b.yaml"}},
			pull:        updated,
			expectDiff:  true,
			diffs:       []gitx.CommitFileDiff{{NewPath: " stacks/b.yaml "}},
			wantOrder:   []string{"b", "a"},
			wantChanged: []string{"b"},
		},
		{
			name:        "deleted file falls back to old path",
			stacks:      []config.StackSpec{{Name: "a", ComposeFile: "a.yaml"}, {Name: "b", ComposeFile: "b.yaml"}},
			pull:        updated,
			expectDiff:  true,
			diffs:       []gitx.CommitFileDiff{{OldPath: "b.yaml"}},
			wantOrder:   []string{"b", "a"},
			wantChanged: []string{"b"},
		},
		{
			name:       "stack without compose file is never matched",
			stacks:     []config.StackSpec{{Name: "a"}, {Name: "b", ComposeFile: "b.yaml"}},
			pull:       updated,
			expectDiff: true,
			diffs:      []gitx.CommitFileDiff{{}},
			wantOrder:  []string{"a", "b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			diffSource := NewMockDiffSource(ctrl)
			if tc.expectDiff {
				diffSource.EXPECT().
					Diff(gomock.Any(), tc.pull.OldRevision, tc.pull.NewRevision).
					Return(tc.diffs, tc.diffErr)
			}
			configured := slices.Clone(tc.stacks)

			plan := NewPlanner(diffSource).Plan(t.Context(), tc.stacks, tc.pull)

			if tc.wantDiffErr != nil {
				require.ErrorIs(t, plan.DiffErr, tc.wantDiffErr, "diff error")
			} else {
				require.NoError(t, plan.DiffErr, "diff error")
			}

			order := make([]string, 0, len(plan.Stacks))
			for _, stack := range plan.Stacks {
				order = append(order, stack.Name)
			}
			assert.Equal(t, tc.wantOrder, order, "stack order")

			changed := make([]string, 0, len(plan.Changed))
			for name := range plan.Changed {
				changed = append(changed, name)
			}
			assert.ElementsMatch(t, tc.wantChanged, changed, "changed stacks")

			assert.Equal(t, configured, tc.stacks, "input stacks must not be reordered")
		})
	}
}
