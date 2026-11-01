package converter

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"go.uber.org/mock/gomock"
)

func TestConvertInvalidAndUnsupportedOptions(t *testing.T) {
	for _, tc := range []struct{ name, yaml, field string }{
		{"top option", "include: other.yaml", "compose.include"},
		{"build", "services: {app: {image: alpine, build: .}}", "service.build"},
		{"service option", "services: {app: {image: alpine, devices: [/dev/null]}}", "service.devices"},
		{"health option", "services: {app: {image: alpine, healthcheck: {unknown: sensitive}}}", "healthcheck.unknown"},
		{"logging option", "services: {app: {image: alpine, logging: {unknown: sensitive}}}", "logging.unknown"},
		{"deploy option", "services: {app: {image: alpine, deploy: {unknown: sensitive}}}", "deploy.unknown"},
		{"placement option", "services: {app: {image: alpine, deploy: {placement: {unknown: sensitive}}}}", "placement.unknown"},
		{"preference option", "services: {app: {image: alpine, deploy: {placement: {preferences: [{unknown: sensitive}]}}}}", "preferences.unknown"},
		{"resource option", "services: {app: {image: alpine, deploy: {resources: {limits: {devices: sensitive}}}}}", "resources.limits.devices"},
		{"network ipam", "networks: {backend: {ipam: {unknown: sensitive}}}", "networks.backend.ipam"},
		{"external network options", "networks: {edge: {external: true, driver: overlay}}", "networks.edge"},
		{"volume option", "volumes: {data: {unknown: sensitive}}", "volumes.data.unknown"},
		{"external volume options", "volumes: {data: {external: true, driver: local}}", "volumes.data"},
		{"config option", "configs: {cfg: {unknown: sensitive}}", "configs.cfg.unknown"},
		{"secret option", "secrets: {key: {unknown: sensitive}}", "secrets.key.unknown"},
		{"image missing", "services: {app: {}}", "image"},
		{"image invalid", "services: {app: {image: 'http://sensitive'}}", "image"},
		{"unprepared env file", "services: {app: {image: alpine, env_file: env}}", "env_file"},
		{"mode", "services: {app: {image: alpine, deploy: {mode: unknown}}}", "deploy.mode"},
		{"global replicas", "services: {app: {image: alpine, deploy: {mode: global, replicas: 2}}}", "deploy.replicas"},
		{"endpoint", "services: {app: {image: alpine, deploy: {endpoint_mode: unknown}}}", "deploy.endpoint_mode"},
		{"bad cpus", "services: {app: {image: alpine, deploy: {resources: {limits: {cpus: sensitive}}}}}", "cpus"},
		{"fraction cpus", "services: {app: {image: alpine, deploy: {resources: {limits: {cpus: '1/2'}}}}}", "cpus"},
		{"negative cpus", "services: {app: {image: alpine, deploy: {resources: {limits: {cpus: '-1'}}}}}", "cpus"},
		{"overflow cpus", "services: {app: {image: alpine, deploy: {resources: {limits: {cpus: '1e30'}}}}}", "cpus"},
		{"memory", "services: {app: {image: alpine, deploy: {resources: {limits: {memory: sensitive}}}}}", "memory"},
		{"pids reservation", "services: {app: {image: alpine, deploy: {resources: {reservations: {pids: 1}}}}}", "reservations.pids"},
		{"restart", "services: {app: {image: alpine, deploy: {restart_policy: {condition: always}}}}", "restart_policy.condition"},
		{"restart delay", "services: {app: {image: alpine, deploy: {restart_policy: {delay: '-1s'}}}}", "restart_policy.delay"},
		{"restart window", "services: {app: {image: alpine, deploy: {restart_policy: {window: sensitive}}}}", "restart_policy.window"},
		{"update action", "services: {app: {image: alpine, deploy: {update_config: {failure_action: stop}}}}", "update_config"},
		{"rollback action", "services: {app: {image: alpine, deploy: {rollback_config: {failure_action: rollback}}}}", "rollback_config"},
		{"update ratio", "services: {app: {image: alpine, deploy: {update_config: {max_failure_ratio: 2}}}}", "max_failure_ratio"},
		{"update delay", "services: {app: {image: alpine, deploy: {update_config: {delay: sensitive}}}}", "update_config"},
		{"update order", "services: {app: {image: alpine, deploy: {update_config: {order: unknown}}}}", "update_config"},
		{"health conflict", "services: {app: {image: alpine, healthcheck: {disable: true, test: [NONE]}}}", "healthcheck"},
		{"health test", "services: {app: {image: alpine, healthcheck: {test: [unknown]}}}", "healthcheck.test"},
		{"health NONE args", "services: {app: {image: alpine, healthcheck: {test: [NONE, sensitive]}}}", "healthcheck.test"},
		{"health duration", "services: {app: {image: alpine, healthcheck: {interval: 1ns}}}", "healthcheck.interval"},
		{"port out of range", "services: {app: {image: alpine, ports: [{target: 65536}]}}", "ports"},
		{"port host ip", "services: {app: {image: alpine, ports: [{target: 80, host_ip: sensitive}]}}", "ports.host_ip"},
		{"port app protocol", "services: {app: {image: alpine, ports: [{target: 80, app_protocol: http}]}}", "ports.host_ip/app_protocol"},
		{"port protocol", "services: {app: {image: alpine, ports: [{target: 80, protocol: unknown}]}}", "ports.protocol"},
		{"port mode", "services: {app: {image: alpine, ports: [{target: 80, mode: unknown}]}}", "ports.mode"},
		{"dnsrr ingress", "services: {app: {image: alpine, ports: [80], deploy: {endpoint_mode: dnsrr}}}", "ports.mode"},
		{"undefined init network", "services: {app: {image: alpine, x-init-deploy-jobs: [{name: migration, image: alpine, networks: [missing]}]}}", "x-init-deploy-jobs.migration.networks.missing"},
		{"undefined network", "services: {app: {image: alpine, networks: [missing]}}", "networks.missing"},
		{"static IP", "services: {app: {image: alpine, networks: {default: {ipv4_address: '10.1.1.2'}}}}", "networks.default.ipv4_address"},
		{"network attachment option", "services: {app: {image: alpine, networks: {default: {priority: 100}}}}", "networks.default.priority"},
		{"duplicate config target", "services: {app: {image: alpine, configs: [{source: cfg, target: /same}, {source: other, target: /same}]}}", "configs.other.target"},
		{"duplicate secret target", "services: {app: {image: alpine, secrets: [key, key]}}", "secrets.key.target"},
		{"undefined config", "services: {app: {image: alpine, configs: [missing]}}", "configs.missing"},
		{"undefined secret", "services: {app: {image: alpine, secrets: [missing]}}", "secrets.missing"},
		{"bad config mode", "services: {app: {image: alpine, configs: [{source: cfg, mode: 07777}]}}", "mode"},
		{"bad owner", "services: {app: {image: alpine, secrets: [{source: key, uid: sensitive}]}}", "uid"},
		{"relative mount target", "services: {app: {image: alpine, volumes: [relative]}}", "volumes.target"},
		{"relative bind", "services: {app: {image: alpine, volumes: ['./data:/data']}}", "bind.source"},
		{"undefined volume", "services: {app: {image: alpine, volumes: ['missing:/data']}}", "undefined named volume"},
		{"mount type", "services: {app: {image: alpine, volumes: [{type: image, source: image, target: /data}]}}", "type"},
		{"mount consistency", "services: {app: {image: alpine, volumes: [{target: /data, consistency: unknown}]}}", "consistency"},
		{"short mount option", "services: {app: {image: alpine, volumes: ['data:/data:Z']}}", "volumes.mode"},
		{"bind options", "services: {app: {image: alpine, volumes: [{type: bind, source: /host, target: /data, volume: {nocopy: true}}]}}", "bind"},
		{"bind host creation", "services: {app: {image: alpine, volumes: [{type: bind, source: /host, target: /data, bind: {create_host_path: true}}]}}", "bind.create_host_path"},
		{"tmpfs source", "services: {app: {image: alpine, volumes: [{type: tmpfs, source: sensitive, target: /tmp}]}}", "tmpfs"},
		{"tmpfs size", "services: {app: {image: alpine, volumes: [{type: tmpfs, target: /tmp, tmpfs: {size: sensitive}}]}}", "tmpfs.size"},
		{"logging without driver", "services: {app: {image: alpine, logging: {options: {max-size: 10m}}}}", "logging.options"},
		{"bad stop grace period", "services: {app: {image: alpine, stop_grace_period: invalid}}", "stop_grace_period"},
		{"bad command quoting", `services: {app: {image: alpine, command: "echo 'sensitive"}}`, "command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converter, networks, _, _ := newTestConverter(t)
			networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil).AnyTimes()
			desired, parseErr := compose.Parse([]byte(tc.yaml))
			if parseErr != nil {
				assert.Contains(t, parseErr.Error(), strings.TrimPrefix(tc.field, "service."))
				return
			}
			require.ErrorContains(t, converter.Validate("demo", *desired), tc.field)
			result, err := converter.Convert(context.Background(), "demo", *desired)
			require.ErrorContains(t, err, tc.field)
			assert.Nil(t, result)
			assert.NotContains(t, err.Error(), "sensitive")
		})
	}
}

func TestConvertRejectsMalformedTypedInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		desired compose.Compose
		field   string
	}{
		{"nil config", compose.Compose{Configs: compose.Configs{"cfg": nil}}, "configs.cfg"},
		{"nil secret", compose.Compose{Secrets: compose.Secrets{"key": nil}}, "secrets.key"},
		{"duplicate services", compose.Compose{Services: compose.Services{{Name: "a", Image: "alpine"}, {Name: "a", Image: "alpine"}}}, "services"},
		{"bad service name", compose.Compose{Services: compose.Services{{Name: "bad name", Image: "alpine"}}}, "services"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converter, _, _, _ := newTestConverter(t)
			_, err := converter.Convert(context.Background(), "demo", tc.desired)
			require.ErrorContains(t, err, tc.field)
		})
	}
	converter, _, _, _ := newTestConverter(t)
	_, err := converter.Convert(context.Background(), "", compose.Compose{})
	require.ErrorContains(t, err, "stackName")
}

func TestUpdatePolicyRejectsNonfiniteRatio(t *testing.T) {
	for _, ratio := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := updatePolicy(nil, "", "", "", &ratio, "", false)
		require.ErrorContains(t, err, "max_failure_ratio")
	}
}
