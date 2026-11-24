package model

import (
	"slices"
	"strings"
)

// QualifiedName identifies a catalog service by stack and name.
func (i Info) QualifiedName() string {
	return strings.TrimSpace(i.Stack) + "/" + strings.TrimSpace(i.Name)
}

// MatchServiceTarget returns the most specific matches for a catalog target.
// A qualified name takes precedence over a service name, stack name and partial match.
func MatchServiceTarget(services []Info, target string) []Info {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return nil
	}
	var names, stacks, partial []Info
	for _, item := range services {
		qualified := item.QualifiedName()
		switch {
		case strings.EqualFold(qualified, target):
			return []Info{item}
		case strings.EqualFold(item.Name, target):
			names = append(names, item)
		case strings.EqualFold(item.Stack, target):
			stacks = append(stacks, item)
		case strings.Contains(strings.ToLower(qualified), target):
			partial = append(partial, item)
		}
	}
	if len(names) > 0 {
		return names
	}
	if len(stacks) > 0 {
		return stacks
	}
	return partial
}

// QualifiedNames returns sorted, unique choices for service selection.
func QualifiedNames(services []Info) []string {
	names := make([]string, 0, len(services))
	for _, service := range services {
		names = append(names, service.QualifiedName())
	}
	slices.Sort(names)
	return slices.Compact(names)
}
