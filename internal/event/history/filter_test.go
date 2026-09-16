package history

import (
	"testing"
	"time"

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
		nil,
	)

	require.Len(t, filtered, 2, "expected selected event types")
	assert.Equal(t, "2", filtered[0].Message, "expected deploy failed event")
	assert.Equal(t, "3", filtered[1].Message, "expected user authenticated event")
}

func TestFilterEntriesFiltersBySince(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Type: events.TypeDeploySuccess, Severity: events.SeverityInfo, Category: events.CategorySync, CreatedAt: since.Add(-time.Second), Message: "old"},
		{Type: events.TypeDeployFailed, Severity: events.SeverityAlert, Category: events.CategorySync, CreatedAt: since, Message: "boundary"},
		{Type: events.TypeUserAuthenticated, Severity: events.SeverityInfo, Category: events.CategorySecurity, CreatedAt: since.Add(time.Second), Message: "new"},
	}

	filtered := FilterEntries(entries, nil, nil, nil, &since)

	require.Len(t, filtered, 2, "expected entries at or after since")
	assert.Equal(t, "boundary", filtered[0].Message, "expected boundary event")
	assert.Equal(t, "new", filtered[1].Message, "expected newer event")
}
