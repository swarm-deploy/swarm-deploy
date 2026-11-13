package modelstore

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"

	"github.com/doug-martin/goqu/v9"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
)

type stackStateRow struct {
	Stack                   string         `db:"stack"`
	DesiredDigest           string         `db:"desired_digest"`
	DesiredVersion          int            `db:"desired_version"`
	DesiredCommit           string         `db:"desired_commit"`
	AppliedDigest           sql.NullString `db:"applied_digest"`
	AppliedVersion          sql.NullInt64  `db:"applied_version"`
	AppliedCommit           sql.NullString `db:"applied_commit"`
	DesiredResourcesDigest  string         `db:"desired_resources_digest"`
	DesiredResourcesVersion int            `db:"desired_resources_version"`
	AppliedResourcesDigest  sql.NullString `db:"applied_resources_digest"`
	AppliedResourcesVersion sql.NullInt64  `db:"applied_resources_version"`
	ResourcesUncertain      bool           `db:"resources_uncertain"`
}

type serviceStateRow struct {
	Stack            string         `db:"stack"`
	Service          string         `db:"service"`
	DesiredDigest    sql.NullString `db:"desired_digest"`
	DesiredVersion   sql.NullInt64  `db:"desired_version"`
	DesiredCommit    string         `db:"desired_commit"`
	AppliedDigest    sql.NullString `db:"applied_digest"`
	AppliedVersion   sql.NullInt64  `db:"applied_version"`
	AppliedCommit    sql.NullString `db:"applied_commit"`
	AppliedUncertain bool           `db:"applied_uncertain"`
}

// SaveDesiredState records the current effective desired hashes without advancing applied evidence.
func (s *Store) SaveDesiredState(
	ctx context.Context,
	stack string,
	commit string,
	desired deploymentmodel.Prepared,
) (deploymentmodel.HashState, error) {
	if desired.DigestVersion <= 0 || desired.Digest == "" || desired.StackResourcesDigest == "" {
		return deploymentmodel.HashState{}, fmt.Errorf("prepared desired digests are incomplete")
	}
	query, args, err := s.db.Dialect().Insert("deployment_stack_states").Prepared(true).Rows(goqu.Record{
		"stack": stack, "desired_digest": desired.Digest, "desired_version": desired.DigestVersion,
		"desired_commit": commit, "desired_resources_digest": desired.StackResourcesDigest,
		"desired_resources_version": desired.DigestVersion,
	}).OnConflict(goqu.DoUpdate("stack", goqu.Record{
		"desired_digest":            goqu.I("excluded.desired_digest"),
		"desired_version":           goqu.I("excluded.desired_version"),
		"desired_commit":            goqu.I("excluded.desired_commit"),
		"desired_resources_digest":  goqu.I("excluded.desired_resources_digest"),
		"desired_resources_version": goqu.I("excluded.desired_resources_version"),
	})).ToSQL()
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	if _, err = s.db.Exec(ctx, query, args...); err != nil {
		return deploymentmodel.HashState{}, err
	}

	names := slices.Sorted(maps.Keys(desired.ServiceDigests))
	absent := goqu.Ex{"stack": stack}
	if len(names) > 0 {
		absent["service"] = goqu.Op{"notIn": names}
	}
	// A service introduced by an attempt that never reached external execution
	// has neither applied evidence nor uncertainty. If it disappears again,
	// remove that desired-only row before clearing desired fields. Uncertain rows
	// remain durable removals because the failed attempt may have created them.
	query, args, err = s.db.Dialect().Delete("deployment_service_states").Prepared(true).
		Where(absent, goqu.Ex{"applied_digest": nil, "applied_uncertain": false}).ToSQL()
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	if _, err = s.db.Exec(ctx, query, args...); err != nil {
		return deploymentmodel.HashState{}, err
	}
	query, args, err = s.db.Dialect().Update("deployment_service_states").Prepared(true).Set(goqu.Record{
		"desired_digest": nil, "desired_version": nil, "desired_commit": commit,
	}).Where(absent, goqu.Or(
		goqu.C("applied_digest").IsNotNull(), goqu.Ex{"applied_uncertain": true},
	)).ToSQL()
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	if _, err = s.db.Exec(ctx, query, args...); err != nil {
		return deploymentmodel.HashState{}, err
	}

	for _, name := range names {
		digest := desired.ServiceDigests[name]
		if digest == "" {
			return deploymentmodel.HashState{}, fmt.Errorf("prepared digest for service %s is empty", name)
		}
		query, args, err = s.db.Dialect().Insert("deployment_service_states").Prepared(true).Rows(goqu.Record{
			"stack": stack, "service": name, "desired_digest": digest,
			"desired_version": desired.DigestVersion, "desired_commit": commit,
		}).OnConflict(goqu.DoUpdate("stack,service", goqu.Record{
			"desired_digest":  goqu.I("excluded.desired_digest"),
			"desired_version": goqu.I("excluded.desired_version"),
			"desired_commit":  goqu.I("excluded.desired_commit"),
		})).ToSQL()
		if err != nil {
			return deploymentmodel.HashState{}, err
		}
		if _, err = s.db.Exec(ctx, query, args...); err != nil {
			return deploymentmodel.HashState{}, err
		}
	}

	query, args, err = s.db.Dialect().Delete("deployment_service_states").Prepared(true).Where(goqu.Ex{
		"stack": stack, "desired_digest": nil, "applied_digest": nil, "applied_uncertain": false,
	}).ToSQL()
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	if _, err = s.db.Exec(ctx, query, args...); err != nil {
		return deploymentmodel.HashState{}, err
	}
	return s.HashState(ctx, stack)
}

// HashState loads the durable desired/applied state for one stack.
func (s *Store) HashState(ctx context.Context, stack string) (deploymentmodel.HashState, error) {
	query, args, err := s.db.Dialect().From("deployment_stack_states").Prepared(true).
		Select(
			"stack", "desired_digest", "desired_version", "desired_commit",
			"applied_digest", "applied_version", "applied_commit",
			"desired_resources_digest", "desired_resources_version",
			"applied_resources_digest", "applied_resources_version", "resources_uncertain",
		).Where(goqu.Ex{"stack": stack}).ToSQL()
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	var row stackStateRow
	if err = s.db.Get(ctx, &row, query, args...); err != nil {
		return deploymentmodel.HashState{}, notFound(err)
	}
	state := deploymentmodel.HashState{
		Stack: row.Stack, DesiredDigest: row.DesiredDigest, DesiredVersion: row.DesiredVersion,
		DesiredCommit: row.DesiredCommit, AppliedDigest: row.AppliedDigest.String,
		AppliedVersion: int(row.AppliedVersion.Int64), AppliedCommit: row.AppliedCommit.String,
		DesiredResourcesDigest:  row.DesiredResourcesDigest,
		DesiredResourcesVersion: row.DesiredResourcesVersion,
		AppliedResourcesDigest:  row.AppliedResourcesDigest.String,
		AppliedResourcesVersion: int(row.AppliedResourcesVersion.Int64),
		ResourcesUncertain:      row.ResourcesUncertain,
		Services:                map[string]deploymentmodel.ServiceHashState{},
	}
	query, args, err = s.db.Dialect().From("deployment_service_states").Prepared(true).
		Select(
			"stack", "service", "desired_digest", "desired_version", "desired_commit",
			"applied_digest", "applied_version", "applied_commit", "applied_uncertain",
		).Where(goqu.Ex{"stack": stack}).Order(goqu.I("service").Asc()).ToSQL()
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	var services []serviceStateRow
	if err = s.db.Select(ctx, &services, query, args...); err != nil {
		return deploymentmodel.HashState{}, err
	}
	for _, service := range services {
		state.Services[service.Service] = deploymentmodel.ServiceHashState{
			Name: service.Service, DesiredDigest: service.DesiredDigest.String,
			DesiredVersion: int(service.DesiredVersion.Int64), DesiredCommit: service.DesiredCommit,
			AppliedDigest: service.AppliedDigest.String, AppliedVersion: int(service.AppliedVersion.Int64),
			AppliedCommit: service.AppliedCommit.String, AppliedUncertain: service.AppliedUncertain,
		}
	}
	return state, nil
}

// MarkAppliedUncertain retains the last confirmed hashes while ensuring an
// unsuccessful external attempt cannot make a later desired-state revert a no-op.
func (s *Store) MarkAppliedUncertain(
	ctx context.Context,
	stack string,
	services []string,
	stackResources bool,
) error {
	if len(services) > 0 {
		query, args, err := s.db.Dialect().Update("deployment_service_states").Prepared(true).
			Set(goqu.Record{"applied_uncertain": true}).
			Where(goqu.Ex{"stack": stack, "service": services}).ToSQL()
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, query, args...); err != nil {
			return err
		}
	}
	if !stackResources {
		return nil
	}
	query, args, err := s.db.Dialect().Update("deployment_stack_states").Prepared(true).
		Set(goqu.Record{"resources_uncertain": true}).Where(goqu.Ex{"stack": stack}).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// ConfirmAppliedState advances only objects confirmed by a successful attempt.
func (s *Store) ConfirmAppliedState(
	ctx context.Context,
	stack string,
	commit string,
	desired deploymentmodel.Prepared,
	appliedServices []string,
	confirmedRemovals []string,
	stackResources bool,
) (deploymentmodel.HashState, error) {
	state, err := s.HashState(ctx, stack)
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	if state.DesiredDigest != desired.Digest || state.DesiredVersion != desired.DigestVersion {
		return deploymentmodel.HashState{}, fmt.Errorf("desired hash state changed during deployment")
	}
	if err = s.confirmAppliedServices(ctx, stack, commit, desired, appliedServices); err != nil {
		return deploymentmodel.HashState{}, err
	}
	if err = s.confirmRemovals(ctx, stack, confirmedRemovals); err != nil {
		return deploymentmodel.HashState{}, err
	}
	if stackResources {
		if err = s.confirmStackResources(ctx, stack, commit, desired); err != nil {
			return deploymentmodel.HashState{}, err
		}
	}

	state, err = s.HashState(ctx, stack)
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	pending := state.Pending()
	objectsApplied := len(pending.Services) == 0 && len(pending.Removals) == 0 &&
		state.DesiredResourcesDigest == state.AppliedResourcesDigest &&
		state.DesiredResourcesVersion == state.AppliedResourcesVersion
	if !objectsApplied {
		return state, nil
	}
	return s.confirmStack(ctx, stack, commit, desired)
}

func (s *Store) confirmAppliedServices(
	ctx context.Context,
	stack string,
	commit string,
	desired deploymentmodel.Prepared,
	services []string,
) error {
	for _, name := range services {
		digest, exists := desired.ServiceDigests[name]
		if !exists {
			return fmt.Errorf("applied service %s is absent from deployment desired state", name)
		}
		query, args, err := s.db.Dialect().Update("deployment_service_states").Prepared(true).Set(goqu.Record{
			"applied_digest": digest, "applied_version": desired.DigestVersion,
			"applied_commit": commit, "applied_uncertain": false,
		}).Where(goqu.Ex{
			"stack": stack, "service": name, "desired_digest": digest,
			"desired_version": desired.DigestVersion,
		}).ToSQL()
		if err != nil {
			return err
		}
		result, err := s.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		if err = requireOneRunning(result); err != nil {
			return fmt.Errorf("confirm service %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) confirmRemovals(ctx context.Context, stack string, removals []string) error {
	for _, name := range removals {
		query, args, err := s.db.Dialect().Delete("deployment_service_states").Prepared(true).Where(goqu.Ex{
			"stack": stack, "service": name, "desired_digest": nil,
		}).ToSQL()
		if err != nil {
			return err
		}
		result, err := s.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		if err = requireOneRunning(result); err != nil {
			return fmt.Errorf("confirm service removal %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) confirmStackResources(
	ctx context.Context,
	stack string,
	commit string,
	desired deploymentmodel.Prepared,
) error {
	query, args, err := s.db.Dialect().Update("deployment_stack_states").Prepared(true).Set(goqu.Record{
		"applied_resources_digest":  desired.StackResourcesDigest,
		"applied_resources_version": desired.DigestVersion,
		"applied_resources_commit":  commit,
		"resources_uncertain":       false,
	}).Where(goqu.Ex{
		"stack": stack, "desired_resources_digest": desired.StackResourcesDigest,
		"desired_resources_version": desired.DigestVersion,
	}).ToSQL()
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if err = requireOneRunning(result); err != nil {
		return fmt.Errorf("confirm stack resources: %w", err)
	}
	return nil
}

func (s *Store) confirmStack(
	ctx context.Context,
	stack string,
	commit string,
	desired deploymentmodel.Prepared,
) (deploymentmodel.HashState, error) {
	query, args, err := s.db.Dialect().Update("deployment_stack_states").Prepared(true).Set(goqu.Record{
		"applied_digest": desired.Digest, "applied_version": desired.DigestVersion,
		"applied_commit": commit,
	}).Where(goqu.Ex{
		"stack": stack, "desired_digest": desired.Digest, "desired_version": desired.DigestVersion,
	}).ToSQL()
	if err != nil {
		return deploymentmodel.HashState{}, err
	}
	if _, err = s.db.Exec(ctx, query, args...); err != nil {
		return deploymentmodel.HashState{}, err
	}
	return s.HashState(ctx, stack)
}
