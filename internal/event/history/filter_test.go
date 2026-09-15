package history

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

func TestFilterEntriesAndOrSemantics(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Type: events.TypeDeploySuccess, Severity: events.SeverityInfo, Category: events.CategorySync, Message: "1"},
		{Type: events.TypeDeployFailed, Severity: events.SeverityAlert, Category: events.CategorySync, Message: "2"},
		{Type: events.TypeUserAuthenticated, Severity: events.SeverityInfo, Category: events.CategorySecurity, Message: "3"},
	}

	filtered := FilterEntries(
		entries,
		[]events.Severity{events.SeverityInfo, events.SeverityAlert},
		[]events.Category{events.CategorySecurity},
		nil,
	)

	assert.Len(t, filtered, 1, "expected AND semantics between filters")
	assert.Equal(t, "3", filtered[0].Message, "expected security info event")
}

func TestFilterEntriesFiltersByType(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Type: events.TypeDeploySuccess, Severity: events.SeverityInfo, Category: events.CategorySync, Message: "1"},
		{Type: events.TypeDeployFailed, Severity: events.SeverityAlert, Category: events.CategorySync, Message: "2"},
		{Type: events.TypeUserAuthenticated, Severity: events.SeverityInfo, Category: events.CategorySecurity, Message: "3"},
	}

	filtered := FilterEntries(
		entries,
		nil,
		nil,
		[]events.TypeName{events.TypeNameDeployFailed, events.TypeNameUserAuthenticated},
	)

	require.Len(t, filtered, 2, "expected selected event types")
	assert.Equal(t, "2", filtered[0].Message, "expected deploy failed event")
	assert.Equal(t, "3", filtered[1].Message, "expected user authenticated event")
}
