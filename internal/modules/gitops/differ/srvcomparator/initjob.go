package srvcomparator

import (
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ/diff"
)

// InitJobComparator compares init jobs by name using service comparators.
type InitJobComparator struct {
	comparators []Comparator
}

// NewInitJobComparator creates an init job comparator from reusable service comparators.
func NewInitJobComparator(comparators []Comparator) *InitJobComparator {
	return &InitJobComparator{
		comparators: comparators,
	}
}

func (i *InitJobComparator) Compare(left, right compose.Service, srvDiff *diff.ServiceDiff) {
	leftJobs := left.MapInitJobs()
	rightJobs := right.MapInitJobs()

	jobNames := make([]string, 0, len(leftJobs)+len(rightJobs))
	seen := make(map[string]struct{}, len(leftJobs)+len(rightJobs))
	for name := range leftJobs {
		seen[name] = struct{}{}
		jobNames = append(jobNames, name)
	}
	for name := range rightJobs {
		if _, exists := seen[name]; exists {
			continue
		}
		jobNames = append(jobNames, name)
	}
	sort.Strings(jobNames)

	jobDiffs := make([]diff.ServiceDiff, 0, len(jobNames))
	for _, name := range jobNames {
		leftJob, leftExists := leftJobs[name]
		rightJob, rightExists := rightJobs[name]

		leftService := compose.Service{Name: name}
		if leftExists {
			leftService = initJobService(leftJob)
		}
		rightService := compose.Service{Name: name}
		if rightExists {
			rightService = initJobService(rightJob)
		}

		jobDiff := i.compareServices(leftService, rightService)
		if jobDiff != nil {
			jobDiffs = append(jobDiffs, *jobDiff)
		}
	}

	srvDiff.InitJobs = jobDiffs
}

func (i *InitJobComparator) compareServices(left, right compose.Service) *diff.ServiceDiff {
	serviceName := right.Name
	if serviceName == "" {
		serviceName = left.Name
	}

	serviceDiff := &diff.ServiceDiff{ServiceName: serviceName}
	for _, comparator := range i.comparators {
		comparator.Compare(left, right, serviceDiff)
	}

	serviceDiff.CalcHasChanges()
	if !serviceDiff.HasChanges {
		return nil
	}

	return serviceDiff
}

func initJobService(job compose.InitJob) compose.Service {
	return compose.Service{
		Name:        job.Name,
		Image:       job.Image,
		Command:     compose.NewCommand(job.Command),
		Environment: job.Environment,
		Networks:    job.Networks,
		Secrets:     job.Secrets,
		Configs:     job.Configs,
	}
}
