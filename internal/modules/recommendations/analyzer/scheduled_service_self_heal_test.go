package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestScheduledServiceSelfHealAnalyzer(t *testing.T) {
	tests := []struct {
		name          string
		containerCron bool
		global        bool
		stack         string
		service       string
		managed       string
		cron          string
		mode          string
		want          int
		wantSelfHeal  bool
	}{
		{name: "replicas disabled only", global: true, managed: "false", cron: "true", want: 1, wantSelfHeal: true},
		{name: "both disabled", global: true, service: "false", managed: "false", cron: "true"},
		{name: "effective self heal disabled and replicas disabled", managed: "off", cron: "true"},
		{name: "explicit managed", service: "false", managed: "true", cron: "true", want: 1},
		{name: "global enabled", global: true, cron: "true", want: 1, wantSelfHeal: true},
		{name: "global disabled", cron: "true", want: 1},
		{name: "stack disables global", global: true, stack: "false", cron: "true", want: 1},
		{name: "stack enables global", stack: "true", cron: "true", want: 1, wantSelfHeal: true},
		{name: "service enables over disabled stack", stack: "false", service: "true", cron: "true", want: 1, wantSelfHeal: true},
		{name: "service disables over enabled stack", global: true, stack: "true", service: "false", cron: "true", want: 1},
		{name: "service disables global", global: true, service: "off", cron: "true", want: 1},
		{name: "service enables global", service: "yes", cron: "true", want: 1, wantSelfHeal: true},
		{name: "cron disabled", global: true, cron: "false"},
		{name: "ordinary service", global: true},
		{name: "native replicated job", global: true, mode: "replicated-job"},
		{name: "native global job", global: true, mode: "global-job"},
		{name: "container label only", global: true, containerCron: true},
	}
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Spec: config.Spec{
				Sync:         config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: tt.global}},
				StacksSource: config.StacksSourceSpec{File: "stacks.yaml"},
			}}
			reloadRecommendationPolicy(t, cfg, t.TempDir(), tt.stack)
			labels := map[string]string{}
			if tt.cron != "" {
				labels[swarmCronjobEnableLabel] = tt.cron
			}
			if tt.service != "" {
				labels[labelsdict.ServiceSyncPolicySelfHealLabelKey] = tt.service
			}
			if tt.managed != "" {
				labels[labelsdict.ServiceSyncPolicyReplicasManagedLabelKey] = tt.managed
			}
			containerLabels := map[string]string{}
			if tt.containerCron {
				containerLabels[swarmCronjobEnableLabel] = "true"
			}
			stack := recommendationTestStack(compose.Service{
				Name:   "worker",
				Labels: *compose.NewLabels(containerLabels),
				Deploy: compose.ServiceDeploy{Mode: tt.mode, Labels: *compose.NewLabels(labels)},
			})
			analyzer := NewScheduledServiceSelfHealAnalyzer(cfg)
			analyzer.now = func() time.Time { return now }
			recs := analyzer.Analyze(t.Context(), stack)
			require.Len(t, recs, tt.want)
			if tt.want == 0 {
				return
			}
			assert.Equal(t, model.Type("service.self-heal.scheduled-service"), recs[0].Type)
			assert.Equal(t, model.SeverityMedium, recs[0].Severity)
			assert.Equal(t, model.Subject{Stack: "payments", Service: "worker"}, recs[0].Subject)
			assert.Equal(t, model.SourceFromStack(stack), recs[0].Source)
			assert.Equal(t, now, recs[0].CreatedAt)
			assert.Equal(t, "Disable self-healing for scheduled service", recs[0].Title)
			assert.Contains(t, recs[0].Recommendation, "Automatic self-healing may interfere")
			if tt.wantSelfHeal {
				assert.Contains(t, recs[0].Recommendation, labelsdict.ServiceSyncPolicySelfHealLabelKey+`: "false"`)
			} else {
				assert.NotContains(t, recs[0].Recommendation, labelsdict.ServiceSyncPolicySelfHealLabelKey+`: "false"`)
			}
			if tt.managed == "false" {
				assert.NotContains(t, recs[0].Recommendation, labelsdict.ServiceSyncPolicyReplicasManagedLabelKey)
			} else {
				assert.Contains(t, recs[0].Recommendation, labelsdict.ServiceSyncPolicyReplicasManagedLabelKey)
			}
		})
	}
}

func TestScheduledServiceSelfHealAnalyzerUsesCurrentConfiguration(t *testing.T) {
	cfg := &config.Config{Spec: config.Spec{
		Sync:         config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: true}},
		StacksSource: config.StacksSourceSpec{File: "stacks.yaml"},
	}}
	dir := t.TempDir()
	analyzer := NewScheduledServiceSelfHealAnalyzer(cfg)
	stack := recommendationTestStack(compose.Service{Name: "worker", Deploy: compose.ServiceDeploy{
		Labels: *compose.NewLabels(map[string]string{swarmCronjobEnableLabel: "true", labelsdict.ServiceSyncPolicyReplicasManagedLabelKey: "false"}),
	}})
	for _, policy := range []string{"false", "true", "false"} {
		reloadRecommendationPolicy(t, cfg, dir, policy)
		assert.Equal(t, policy == "true", len(analyzer.Analyze(t.Context(), stack)) == 1)
	}
	reloadRecommendationPolicy(t, cfg, dir, "")
	require.Len(t, analyzer.Analyze(t.Context(), stack), 1)
	cfg.Spec.Sync.Policy.SelfHeal = false
	assert.Empty(t, analyzer.Analyze(t.Context(), stack))
}

func reloadRecommendationPolicy(t *testing.T, cfg *config.Config, dir, policy string) {
	t.Helper()
	content := "stacks:\n  - name: payments\n    composeFile: compose.yaml\n"
	if policy != "" {
		content += fmt.Sprintf("    sync:\n      policy:\n        selfHeal: %s\n", policy)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(content), 0o600))
	_, err := cfg.Reload(dir)
	require.NoError(t, err)
}
