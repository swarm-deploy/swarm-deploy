package codec

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

type classifiedError struct {
	message   string
	temporary bool
}

func (e classifiedError) Error() string   { return e.message }
func (e classifiedError) Temporary() bool { return e.temporary }

func TestSensitiveFieldsAreNeverPersisted(t *testing.T) {
	const secret = "super-private-token"
	service := compose.Service{Name: "api", Image: "nginx:stable",
		Environment: compose.Environment{Map: map[string]string{"TOKEN": secret}},
		Extra:       map[string]any{"password": secret},
	}
	for _, event := range []events.Event{
		&events.DeploySuccess{DeployEvent: events.DeployEvent{Services: []compose.Service{service}}},
		&events.DeployFailed{DeployEvent: events.DeployEvent{StackName: "stack", Commit: "revision", StackDefinition: compose.File{Path: secret}}, Error: errors.New(secret), Logs: []string{secret}},
		&events.DeployPreparationFailed{StackName: "stack", Commit: "revision", ErrorCode: "preparation_failed"},
		&events.DeployInterrupted{DeploymentID: "deployment", StackName: "stack", Commit: "revision", Reason: "process_interrupted"},
		&events.AssistantPromptInjectionDetected{Prompt: secret},
		&events.SendNotificationFailed{Error: errors.New(secret)},
	} {
		t.Run(event.Type().String(), func(t *testing.T) {
			payload, err := Encode(event)
			require.NoError(t, err)
			assert.NotContains(t, string(payload), secret)
			assert.NotContains(t, string(payload), "StackDefinition")
			assert.NotContains(t, string(payload), "Environment")
			decoded, err := Decode(event.Type().Name(), Version, payload)
			require.NoError(t, err)
			for _, value := range decoded.Details() {
				assert.NotContains(t, value, secret)
			}
		})
	}
}

func TestNotificationFailureCodecCompatibility(t *testing.T) {
	event := &events.SendNotificationFailed{
		EventType: events.TypeAlertOpened, Destination: "telegram", Channel: "alerts",
		DestinationKey: "telegram:safe-hash", SourceDestinationKey: "telegram:source-hash",
		Error: errors.New("handler_failed"),
	}
	payload, err := Encode(event)
	require.NoError(t, err)
	decoded, err := Decode(events.TypeNameSendNotificationFailed, Version, payload)
	require.NoError(t, err)
	failure := decoded.(*events.SendNotificationFailed)
	assert.Equal(t, events.TypeNameAlertOpened, failure.EventType.Name())
	assert.Equal(t, "telegram:safe-hash", failure.DestinationKey)
	assert.Equal(t, "telegram:source-hash", failure.SourceDestinationKey)
	assert.EqualError(t, failure.Error, "handler_failed")

	decoded, err = Decode(events.TypeNameSendNotificationFailed, Version,
		[]byte(`{"event_type":"AlertOpened","destination":"telegram","channel":"alerts"}`))
	require.NoError(t, err)
	assert.NoError(t, decoded.(*events.SendNotificationFailed).Error)
	assert.Empty(t, decoded.(*events.SendNotificationFailed).DestinationKey)
	assert.Empty(t, decoded.(*events.SendNotificationFailed).SourceDestinationKey)
}

func TestNotificationSentCodec(t *testing.T) {
	deliveredAt := time.Date(2026, 10, 25, 8, 30, 0, 0, time.UTC)
	event := &events.NotificationSent{
		EventType: events.TypeAlertOpened, Destination: "telegram", Channel: "alerts",
		DestinationKey: "telegram:safe-hash", DeliveredAt: deliveredAt,
	}
	payload, err := Encode(event)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "chat-id")
	decoded, err := Decode(events.TypeNameNotificationSent, Version, payload)
	require.NoError(t, err)
	assert.Equal(t, event, decoded)
}

func TestDeploymentLifecycleCodecRejectsUnboundedDiagnostics(t *testing.T) {
	for _, event := range []events.Event{
		&events.DeployPreparationFailed{StackName: "stack", ErrorCode: "raw secret"},
		&events.DeployInterrupted{StackName: "stack", Reason: "raw secret"},
	} {
		_, err := Encode(event)
		require.Error(t, err)
	}
}

func TestVersionAndPayloadValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     events.TypeName
		version int
		payload string
	}{
		{"future version", events.TypeNameNodeJoined, 2, `{}`},
		{"unknown type", "unknown", 1, `{}`},
		{"unknown field", events.TypeNameNodeJoined, 1, `{"password":"secret"}`},
		{"trailing data", events.TypeNameNodeJoined, 1, `{} {}`},
		{"null payload", events.TypeNameNodeJoined, 1, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) { _, err := Decode(tc.typ, tc.version, []byte(tc.payload)); require.Error(t, err) })
	}
}

func TestConfigurationChangedCodecHasNoDomainPayload(t *testing.T) {
	payload, err := Encode(&events.ConfigurationChanged{})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(payload))
	decoded, err := Decode(events.TypeNameConfigurationChanged, Version, payload)
	require.NoError(t, err)
	assert.IsType(t, &events.ConfigurationChanged{}, decoded)
	assert.Empty(t, decoded.Details())
}

func TestConfigurationChangedCodecCarriesSafeRepositoryTransition(t *testing.T) {
	event := &events.ConfigurationChanged{
		PreviousRepository: "git.example.com/team/old", Repository: "git.example.com/team/new",
	}
	payload, err := Encode(event)
	require.NoError(t, err)
	decoded, err := Decode(events.TypeNameConfigurationChanged, Version, payload)
	require.NoError(t, err)
	assert.Equal(t, event, decoded)

	_, err = Encode(&events.ConfigurationChanged{
		PreviousRepository: "https://user:secret@git.example.com/old", Repository: "git.example.com/team/new",
	})
	require.Error(t, err)
}

func TestAlertLifecycleCodecCarriesDeploymentID(t *testing.T) {
	event := &events.AlertOpened{Alert: events.AlertSnapshot{
		ID: "alert", Kind: "sync_failed", ResourceType: "stack", ResourceID: "app",
		DeploymentID: "deployment-1",
	}}
	payload, err := Encode(event)
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"deploymentId":"deployment-1"`)

	decoded, err := Decode(events.TypeNameAlertOpened, Version, payload)
	require.NoError(t, err)
	assert.Equal(t, "deployment-1", decoded.(*events.AlertOpened).Alert.DeploymentID)
}

func TestGitPullLifecycleCodec(t *testing.T) {
	failed := &events.GitPullFailed{
		Repository: "git.example.com/team/app", Category: events.GitFailureAuthentication,
		Description: "token=must-not-be-copied", TriggerReason: "webhook",
	}
	payload, err := Encode(failed)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "must-not-be-copied")
	assert.Contains(t, string(payload), "Git repository authentication failed")
	decoded, err := Decode(events.TypeNameGitPullFailed, Version, payload)
	require.NoError(t, err)
	assert.Equal(t, "git.example.com/team/app", decoded.(*events.GitPullFailed).Repository)
	assert.Equal(t, events.GitFailureAuthentication, decoded.(*events.GitPullFailed).Category)
	assert.Equal(t, "webhook", decoded.(*events.GitPullFailed).TriggerReason)

	succeeded := &events.GitPullSucceeded{Repository: "git.example.com/team/app", TriggerReason: "poll"}
	payload, err = Encode(succeeded)
	require.NoError(t, err)
	decoded, err = Decode(events.TypeNameGitPullSucceeded, Version, payload)
	require.NoError(t, err)
	assert.Equal(t, succeeded, decoded)
}

func TestGitPullCodecRejectsUnsafePayloads(t *testing.T) {
	for _, event := range []events.Event{
		&events.GitPullFailed{Repository: "https://user:secret@git.example.com/team/app", Category: events.GitFailureOther},
		&events.GitPullFailed{Repository: "https://user:secret%zz@git.example.com/team/app", Category: events.GitFailureOther},
		&events.GitPullFailed{Repository: "git.example.com/team/app", Category: "unclassified"},
		&events.GitPullSucceeded{Repository: "https://git.example.com/team/app?token=secret"},
	} {
		_, err := Encode(event)
		require.Error(t, err)
	}
	_, err := Decode(events.TypeNameGitPullFailed, Version,
		[]byte(`{"repository":"git.example.com/team/app","category":"authentication","description":"secret"}`))
	require.Error(t, err)
}

func TestDeploymentFailureCategorySurvivesCodec(t *testing.T) {
	for _, code := range []string{"policy_rejected", "init_failed", "apply_failed", "prune_failed"} {
		event := &events.DeployFailed{DeployEvent: events.DeployEvent{StackName: "app"}, Error: errors.New(code)}
		payload, err := Encode(event)
		require.NoError(t, err)
		assert.Contains(t, string(payload), code)
		decoded, err := Decode(events.TypeNameDeployFailed, Version, payload)
		require.NoError(t, err)
		assert.Equal(t, code, decoded.(*events.DeployFailed).Error.Error())
	}
	payload, err := Encode(&events.DeployFailed{Error: errors.New("secret diagnostic")})
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "secret diagnostic")
}

func TestDeploymentFailureDiagnosticIsActionableAndMasked(t *testing.T) {
	event := &events.DeployFailed{
		DeployEvent: events.DeployEvent{DeploymentID: "deployment", AttemptID: "attempt-2", StackName: "app", Commit: "sha"},
		ErrorCode:   "apply_failed",
		Stage:       "apply",
		Error:       errors.New("update service api: pull https://user:password@registry.example/image token=private-value: image not found"),
	}
	payload, err := Encode(event)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "password")
	assert.NotContains(t, string(payload), "private-value")
	decoded, err := Decode(events.TypeNameDeployFailed, Version, payload)
	require.NoError(t, err)
	failure := decoded.(*events.DeployFailed)
	assert.Equal(t, "deployment", failure.DeploymentID)
	assert.Equal(t, "attempt-2", failure.AttemptID)
	assert.Equal(t, "apply", failure.Stage)
	assert.Equal(t, "apply_failed", failure.ErrorCode)
	require.Error(t, failure.Error)
	assert.Contains(t, failure.Error.Error(), "update service api")
	assert.Contains(t, failure.Error.Error(), "image not found")
	assert.Contains(t, failure.Error.Error(), "****")
}

func TestDeploymentFailureTemporaryClassificationSurvivesCodec(t *testing.T) {
	tests := []struct {
		name          string
		event         events.Event
		wantTemporary bool
	}{
		{
			name: "deploy failed temporary",
			event: &events.DeployFailed{
				DeployEvent: events.DeployEvent{StackName: "app"},
				Error:       classifiedError{message: "registry unavailable", temporary: true},
			},
			wantTemporary: true,
		},
		{
			name: "preparation failed permanent",
			event: &events.DeployPreparationFailed{
				StackName: "app", ErrorCode: "preparation_failed",
				Error: classifiedError{message: "invalid compose", temporary: false},
			},
			wantTemporary: false,
		},
		{
			name: "deploy interrupted temporary",
			event: &events.DeployInterrupted{
				StackName: "app", Reason: "verification_unknown",
				Error: classifiedError{message: "Docker API unavailable", temporary: true},
			},
			wantTemporary: true,
		},
		{
			name: "unclassified error defaults to permanent",
			event: &events.DeployFailed{
				DeployEvent: events.DeployEvent{StackName: "app"},
				Error:       errors.New("deployment failed"),
			},
			wantTemporary: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			payload, err := Encode(testCase.event)
			require.NoError(t, err)
			assert.NotContains(t, string(payload), "classifiedError")
			assert.Contains(t, string(payload), `"temporary":`)

			decoded, err := Decode(testCase.event.Type().Name(), Version, payload)
			require.NoError(t, err)
			var classified faults.Error
			require.ErrorAs(t, deploymentFailureError(decoded), &classified)
			assert.Equal(t, testCase.wantTemporary, classified.Temporary())
			assert.Equal(t, faults.SafeDiagnostic(deploymentFailureError(testCase.event)), classified.Error())
		})
	}
}

func deploymentFailureError(event events.Event) error {
	switch failure := event.(type) {
	case *events.DeployFailed:
		return failure.Error
	case *events.DeployPreparationFailed:
		return failure.Error
	case *events.DeployInterrupted:
		return failure.Error
	default:
		return nil
	}
}
func TestGitAlertSnapshotDetailsSurviveCodec(t *testing.T) {
	resolvedAt := time.Now().UTC()
	alert := events.AlertSnapshot{
		ID: "git-alert", Kind: "git_repository_unavailable", ResourceType: "repository",
		ResourceID: "git.example.com/team/repo", Message: "Git repository connection timed out",
		ErrorCode: "network", Occurrences: 7, Duration: "2m14s",
		OpenedAt: resolvedAt.Add(-134 * time.Second), ResolvedAt: &resolvedAt,
	}
	for _, event := range []events.Event{
		&events.AlertOpened{Alert: alert},
		&events.AlertResolved{Alert: alert},
	} {
		payload, err := Encode(event)
		require.NoError(t, err)
		decoded, err := Decode(event.Type().Name(), Version, payload)
		require.NoError(t, err)
		var got events.AlertSnapshot
		switch e := decoded.(type) {
		case *events.AlertOpened:
			got = e.Alert
		case *events.AlertResolved:
			got = e.Alert
		}
		assert.Equal(t, alert, got)
	}
}

func TestGitNetworkDiagnosticsSurviveDurableCodec(t *testing.T) {
	for _, description := range []string{
		"Git repository is temporarily unreachable",
		"Git repository connection refused",
		"Git repository connection timed out",
		"Git repository DNS lookup failed",
		"Git repository host unreachable",
		"Git repository network unreachable",
		"Git remote returned HTTP 502 (Bad Gateway)",
		"Git remote returned HTTP 503 (Service Unavailable)",
		"Git remote returned HTTP 504 (Gateway Timeout)",
	} {
		t.Run(description, func(t *testing.T) {
			original := &events.GitPullFailed{
				Repository: "git.example.com/team/app", Category: events.GitFailureNetwork,
				Description: description, TriggerReason: "manual",
			}
			payload, err := Encode(original)
			require.NoError(t, err)
			decoded, err := Decode(events.TypeNameGitPullFailed, Version, payload)
			require.NoError(t, err)
			assert.Equal(t, description, decoded.(*events.GitPullFailed).Description)
		})
	}
}

func TestGitNetworkDiagnosticsDoNotPersistUnsafeDescriptions(t *testing.T) {
	const secret = "must-not-leak"
	original := &events.GitPullFailed{
		Repository: "git.example.com/team/app", Category: events.GitFailureNetwork,
		Description: "Git repository connection refused; token=" + secret,
	}
	payload, err := Encode(original)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), secret)
	decoded, err := Decode(events.TypeNameGitPullFailed, Version, payload)
	require.NoError(t, err)
	assert.Equal(t, "Git repository is temporarily unreachable", decoded.(*events.GitPullFailed).Description)
	_, err = Decode(events.TypeNameGitPullFailed, Version, []byte(
		`{"repository":"git.example.com/team/app","category":"network","description":"Git repository connection refused; token=must-not-leak"}`,
	))
	require.Error(t, err)
}
