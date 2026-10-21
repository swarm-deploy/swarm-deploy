package logparser

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Query configures task log retrieval, optional parsing, and backend filtering.
type Query struct {
	// Follow keeps the Docker log stream open for new entries.
	Follow bool
	// Limit is the maximum number of recent Docker log lines to inspect.
	Limit int
	// Since is an optional inclusive lower timestamp bound.
	Since *time.Time
	// Until is an optional inclusive upper timestamp bound.
	Until *time.Time
	// Parse enables JSON/logfmt normalization and level filtering.
	Parse bool
	// Levels selects normalized levels. Empty means all levels.
	Levels []Level
	// Attributes selects entries containing every exact attribute key-value pair.
	Attributes map[string]any
}

// StreamItem contains one processed source entry and current backend level counts.
type StreamItem struct {
	// Entry is the normalized or backward-compatible raw entry.
	Entry Entry
	// Included reports whether Entry matched the backend level filter.
	Included bool
	// Counts contains counts for all parsed levels seen in this stream.
	Counts map[Level]int
}

// Result contains a bounded non-following task log query response.
type Result struct {
	// Entries contains entries accepted by the backend filter.
	Entries []Entry
	// Counts contains counts for all parsed levels in the inspected window.
	Counts map[Level]int
}

// Service shares task log parsing and filtering between API and assistant consumers.
type Service struct {
	source swarm.ServiceManager
	parser *Parser
}

// NewService creates a task log service backed by existing Docker log retrieval.
func NewService(source swarm.ServiceManager) *Service {
	return &Service{
		source: source,
		parser: NewParser(),
	}
}

// ParseLevels validates and normalizes a list of level filters.
func ParseLevels(values []string) ([]Level, error) {
	parsed := make([]Level, 0, len(values))
	seen := make(map[Level]struct{}, len(values))
	for _, value := range values {
		level, ok := ParseLevel(value)
		if !ok {
			return nil, fmt.Errorf("unsupported log level %q", value)
		}
		if _, exists := seen[level]; exists {
			continue
		}
		seen[level] = struct{}{}
		parsed = append(parsed, level)
	}

	return parsed, nil
}

// Stream returns processed task logs while preserving Docker stream metadata.
func (s *Service) Stream(
	ctx context.Context,
	taskID string,
	query Query,
) (<-chan StreamItem, <-chan error, error) {
	rawEntries, rawErrors, err := s.source.TaskLogs(ctx, taskID, swarm.TaskLogsOptions{
		Follow: query.Follow,
		Limit:  query.Limit,
		Since:  query.Since,
		Until:  query.Until,
	})
	if err != nil {
		return nil, nil, err
	}

	items := make(chan StreamItem)
	errorsChannel := make(chan error, 1)
	selectedLevels := makeLevelSet(query.Levels)

	go s.forwardStream(ctx, query.Parse, selectedLevels, query.Attributes, rawEntries, rawErrors, items, errorsChannel)

	return items, errorsChannel, nil
}

func (s *Service) forwardStream(
	ctx context.Context,
	parse bool,
	selectedLevels map[Level]struct{},
	selectedAttributes map[string]any,
	rawEntries <-chan swarm.LogEntry,
	rawErrors <-chan error,
	items chan<- StreamItem,
	errorsChannel chan<- error,
) {
	defer close(items)
	defer close(errorsChannel)

	counts := emptyLevelCounts()
	for {
		rawEntry, ok := receiveRawEntry(ctx, rawEntries)
		if !ok {
			if ctx.Err() == nil {
				if streamErr := <-rawErrors; streamErr != nil {
					errorsChannel <- streamErr
				}
			}
			return
		}

		item := s.processEntry(rawEntry, parse, selectedLevels, selectedAttributes, counts)
		if !sendStreamItem(ctx, items, item) {
			return
		}
	}
}

func receiveRawEntry(ctx context.Context, rawEntries <-chan swarm.LogEntry) (swarm.LogEntry, bool) {
	select {
	case <-ctx.Done():
		return swarm.LogEntry{}, false
	case rawEntry, ok := <-rawEntries:
		return rawEntry, ok
	}
}

func sendStreamItem(ctx context.Context, items chan<- StreamItem, item StreamItem) bool {
	select {
	case <-ctx.Done():
		return false
	case items <- item:
		return true
	}
}

func (s *Service) processEntry(
	rawEntry swarm.LogEntry,
	parse bool,
	selectedLevels map[Level]struct{},
	selectedAttributes map[string]any,
	counts map[Level]int,
) StreamItem {
	entry := rawTaskLogEntry(rawEntry)
	included := true
	if parse {
		entry = s.parser.Parse(RawEntry{
			Timestamp: rawEntry.Timestamp,
			Stream:    rawEntry.Stream,
			Message:   rawEntry.Message,
		})
		counts[entry.Level]++
		included = matchesLevel(entry.Level, selectedLevels) && matchesAttributes(entry.Attributes, selectedAttributes)
	}

	return StreamItem{
		Entry:    entry,
		Included: included,
		Counts:   cloneCounts(counts),
	}
}

// Get returns a bounded snapshot using the same parser and filter as Stream.
func (s *Service) Get(ctx context.Context, taskID string, query Query) (Result, error) {
	query.Follow = false
	items, errorsChannel, err := s.Stream(ctx, taskID, query)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		Entries: make([]Entry, 0, query.Limit),
		Counts:  emptyLevelCounts(),
	}
	for item := range items {
		result.Counts = item.Counts
		if item.Included && (query.Limit <= 0 || len(result.Entries) < query.Limit) {
			result.Entries = append(result.Entries, item.Entry)
		}
	}
	if streamErr := <-errorsChannel; streamErr != nil {
		return Result{}, streamErr
	}

	return result, nil
}

func rawTaskLogEntry(raw swarm.LogEntry) Entry {
	return Entry{
		Timestamp: raw.Timestamp.UTC(),
		Stream:    raw.Stream,
		Message:   raw.Message,
	}
}

func makeLevelSet(levels []Level) map[Level]struct{} {
	if len(levels) == 0 {
		return nil
	}
	set := make(map[Level]struct{}, len(levels))
	for _, level := range levels {
		set[level] = struct{}{}
	}

	return set
}

func matchesLevel(level Level, selected map[Level]struct{}) bool {
	if len(selected) == 0 {
		return true
	}
	_, ok := selected[level]

	return ok
}

func matchesAttributes(attributes map[string]any, selected map[string]any) bool {
	for key, expected := range selected {
		actual, ok := attributes[key]
		if !ok || !equalJSONValues(actual, expected) {
			return false
		}
	}

	return true
}

func equalJSONValues(left any, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}

	var normalizedLeft any
	if err := json.Unmarshal(leftJSON, &normalizedLeft); err != nil {
		return false
	}
	var normalizedRight any
	if err := json.Unmarshal(rightJSON, &normalizedRight); err != nil {
		return false
	}

	return reflect.DeepEqual(normalizedLeft, normalizedRight)
}

func emptyLevelCounts() map[Level]int {
	counts := make(map[Level]int, len(levels))
	for _, level := range levels {
		counts[level] = 0
	}

	return counts
}

func cloneCounts(counts map[Level]int) map[Level]int {
	cloned := make(map[Level]int, len(counts))
	for level, count := range counts {
		cloned[level] = count
	}

	return cloned
}
