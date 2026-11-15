// Package composetest provides Compose fixtures shared by tests of several packages.
//
//nolint:mnd // Fixture values are literal Compose input.
package composetest

import (
	"context"
	"fmt"
	"os"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// CanonicalFixture is an effective Compose definition whose canonical form and
// desired-state digest are pinned by tests.
type CanonicalFixture struct {
	// Name identifies the fixture in table-driven tests.
	Name string
	// Load returns a fresh copy of the effective Compose file.
	Load func() (compose.File, error)
}

// CanonicalFixtures returns definitions that cover every normalization applied
// by compose.Canonicalize: object references, execution syntax, user map entries
// named like parser metadata, list ordering, capabilities, and models built in code.
func CanonicalFixtures() []CanonicalFixture {
	fixtures := make([]CanonicalFixture, 0, len(yamlFixtures)+len(loadedFixtures)+2)
	for _, fixture := range yamlFixtures {
		raw := fixture.raw
		fixtures = append(fixtures, CanonicalFixture{Name: fixture.name, Load: func() (compose.File, error) {
			parsed, err := compose.Parse([]byte(raw))
			if err != nil {
				return compose.File{}, err
			}
			return compose.File{Path: "compose.yaml", Digest: "source-digest", Compose: *parsed}, nil
		}})
	}
	for _, fixture := range loadedFixtures {
		files := fixture.files
		fixtures = append(fixtures, CanonicalFixture{Name: fixture.name, Load: func() (compose.File, error) {
			loader := compose.NewFileLoaderWithReader(func(_ context.Context, path string) ([]byte, error) {
				content, ok := files[path]
				if !ok {
					return nil, fmt.Errorf("read %s: %w", path, os.ErrNotExist)
				}
				return []byte(content), nil
			})
			file, err := loader.Load(context.Background(), "stack/compose.yaml")
			if err != nil {
				return compose.File{}, err
			}
			return *file, nil
		}})
	}
	return append(fixtures,
		CanonicalFixture{Name: "programmatic model", Load: programmaticFixture},
		CanonicalFixture{Name: "programmatic zero values", Load: func() (compose.File, error) {
			return compose.File{Path: "compose.yaml", Compose: compose.Compose{
				Services: compose.Services{{Name: "zero", Image: "alpine"}},
			}}, nil
		}},
	)
}

var yamlFixtures = []struct {
	name string
	raw  string
}{
	{
		name: "service object references",
		raw: `services:
  api:
    image: alpine
    secrets:
      - token
      - {source: key}
      - {source: cert, target: tls.crt}
      - {source: abs, target: /etc/abs.pem, uid: "0", gid: "0", mode: 0444}
      - {source: owned, target: owned.txt, uid: "1000", gid: "1001", mode: 0400}
      - {source: under, target: /run/secrets/under}
    configs:
      - app
      - {source: nofile}
      - {source: rel, target: rel.conf}
      - {source: abs, target: /etc/app.conf, uid: "0", gid: "0", mode: 0444}
      - {source: owned, target: /etc/owned.conf, uid: "1000", mode: 0640}
      - {source: zero, target: /etc/zero.conf, gid: "0", mode: 0}
secrets:
  token: {external: true}
  key: {external: true}
  cert: {external: true}
  abs: {external: true}
  owned: {external: true}
  under: {external: true}
configs:
  app: {external: true}
  nofile: {external: true}
  rel: {external: true}
  abs: {external: true}
  owned: {external: true}
  zero: {external: true}
`,
	},
	{
		name: "init job object references",
		raw: `services:
  api:
    image: alpine
    secrets: [token, {source: cert, target: tls.crt}]
    configs: [app, {source: nofile}]
    x-init-deploy-jobs:
      - name: migrate
        image: migrator
        entrypoint: [/bin/sh, -c]
        command: [migrate up]
        timeout: 5m
        secrets:
          - token
          - {source: key}
          - {source: cert, target: tls.crt}
          - {source: abs, target: /etc/abs.pem, uid: "0", gid: "0", mode: 0444}
          - {source: owned, target: owned.txt, uid: "1000", gid: "1001", mode: 0400}
        configs:
          - app
          - {source: nofile, uid: "1000", gid: "1000", mode: 0400}
          - {source: rel, target: rel.conf}
          - {source: abs, target: /etc/app.conf, uid: "0", gid: "0", mode: 0444}
          - {source: owned, target: /etc/owned.conf, uid: "1000", mode: 0640}
        environment: {Keys: job-keys, env_file: job-env-file, MODE: migrate}
        networks: [backend, frontend]
      - name: seed
        image: seeder
        command: [seed]
        environment: [Keys=list-keys, B=2]
        networks:
          frontend: {aliases: [seed]}
          backend: {}
secrets:
  token: {external: true}
  key: {external: true}
  cert: {external: true}
  abs: {external: true}
  owned: {external: true}
configs:
  app: {external: true}
  nofile: {external: true}
  rel: {external: true}
  abs: {external: true}
  owned: {external: true}
networks:
  backend: {}
  frontend: {}
`,
	},
	{
		name: "execution syntax",
		raw: `services:
  scalar:
    image: alpine
    command: sh -c 'echo "hello world" && sleep 10'
    entrypoint: /docker-entrypoint.sh --flag "quoted value"
    healthcheck:
      test: curl -f http://localhost/ || exit 1
      interval: 10s
      retries: 3
  list:
    image: alpine
    command: [sleep, "100"]
    entrypoint: [/bin/sh, -c, echo hello]
    healthcheck: {test: [CMD, curl, -f, http://localhost/], timeout: 2s}
  empty:
    image: alpine
    command: []
    entrypoint: ""
    healthcheck: {interval: 30s}
  shell-list:
    image: alpine
    healthcheck: {test: [CMD-SHELL, "pg_isready -U <user>"], start_period: 5s}
  disabled:
    image: alpine
    healthcheck: {disable: true}
  none:
    image: alpine
    healthcheck: {test: [NONE]}
  blank-test:
    image: alpine
    healthcheck: {test: ""}
`,
	},
	{
		name: "environment and labels named like parser metadata",
		raw: `services:
  map:
    image: alpine
    env_file: [app.env, other.env]
    environment:
      Keys: value-keys
      env_file: value-env-file
      Map: value-map
      EMPTY:
      HTML: <a&b>
    labels:
      Map: value-map
      Keys: value-keys
      env_file: value-env-file
      Volumes: value-volumes
      List: value-list
      AliasMap: value-alias-map
      Names: value-names
      Aliases: value-aliases
      short_form: value-short-form
      IsList: value-is-list
    deploy:
      labels: [Map=deploy-map, Keys=deploy-keys, Aliases=deploy-aliases]
  list:
    image: alpine
    environment: [Keys=list-keys, env_file=list-env-file, Z=last, A=first]
    labels: [Names=list-names, AliasMap=list-alias-map, Volumes=list-volumes]
`,
	},
	{
		name: "capabilities",
		raw: `services:
  caps:
    image: alpine
    cap_add: [net_admin, CAP_SYS_TIME, " sys admin", "cap_ net_raw", "<odd>&", "a\"quote", RESET, chown]
    cap_drop: [net_admin, "z z", "<drop>", MKNOD, "CAP_CAP_SYS_ADMIN"]
  all:
    image: alpine
    cap_add: [ALL, net_admin]
    cap_drop: [all]
`,
	},
	{
		name: "service ordering",
		raw: `services:
  zeta: {image: alpine, command: [zeta]}
  alpha: {image: alpine, command: [alpha]}
  mid: {image: alpine, command: [mid]}
  Upper: {image: alpine}
`,
	},
}

var loadedFixtures = []struct {
	name  string
	files map[string]string
}{
	{
		name: "loaded mounts, ports and networks",
		files: map[string]string{
			"stack/compose.yaml": `services:
  web:
    image: nginx:1.27
    volumes:
      - data:/var/lib/data
      - ./config:/etc/nginx/conf.d:ro
      - cache:/cache:nocopy
      - type: tmpfs
        target: /tmp
        tmpfs: {size: 64m, mode: 01777}
      - type: bind
        source: /srv/static
        target: /usr/share/nginx/html
        read_only: true
        bind: {propagation: rshared, create_host_path: true}
      - /anonymous
    ports:
      - "8080:80"
      - "9000-9001:9000-9001/udp"
      - {target: 443, published: 8443, protocol: tcp, mode: host}
      - "5000:5000"
    networks:
      frontend: {aliases: [web, www], ipv4_address: 10.0.0.10}
      backend: {driver_opts: {mtu: "1400"}}
    configs:
      - {source: nginx, target: /etc/nginx/nginx.conf, mode: 0440}
    secrets: [tls]
    env_file: [web.env]
    environment: {MODE: production}
    x-init-deploy-jobs:
      - image: alpine
        command: [prepare]
        networks: [backend]
  worker:
    image: worker:2
    networks: [backend, default]
    volumes: [data:/data, cache:/cache]
    ports: ["7000:7000", "6000:6000"]
volumes:
  data: {}
  cache: {name: shared-cache}
networks:
  frontend: {name: edge}
  backend: {driver: overlay, attachable: true}
configs:
  nginx: {file: ./nginx.conf}
secrets:
  tls: {file: ./tls.pem}
`,
			"stack/nginx.conf": "events {}\n",
			"stack/tls.pem":    "certificate\n",
			"stack/web.env":    "FROM_FILE=yes\nKeys=from-env-file\n",
		},
	},
}

func programmaticFixture() (compose.File, error) {
	environment, err := compose.NewEnvironment([]string{"Keys=programmatic", "B=2", "A=1"})
	if err != nil {
		return compose.File{}, err
	}
	jobEnvironment, err := compose.NewEnvironment([]string{"JOB=1"})
	if err != nil {
		return compose.File{}, err
	}
	defaultMode := os.FileMode(0o444)
	ownMode := os.FileMode(0o400)
	data := &compose.ServiceVolume{Type: compose.ServiceVolumeTypeVolume, Source: "data", Target: "/data"}
	cache := &compose.ServiceVolume{Type: compose.ServiceVolumeTypeVolume, Source: "cache", Target: "/cache"}
	service := compose.Service{
		Name:       "programmatic",
		Image:      "alpine",
		Command:    compose.Command{Args: []string{`sh -c 'echo "built in code"'`}},
		Entrypoint: compose.NewCommand([]string{"/entrypoint.sh"}),
		Healthcheck: &compose.ServiceHealth{
			Test: compose.Command{Args: []string{"curl -f http://localhost/"}}, Interval: "5s",
		},
		CapAdd:  compose.Capabilities{"net_admin", "CAP_NET_ADMIN", "RESET", "<x>"},
		CapDrop: compose.Capabilities{"sys_admin", "net_admin"},
		Secrets: []compose.ObjectRef{
			{Source: "token"},
			{Source: "key", Uid: "0", Gid: "0", Mode: &defaultMode},
			{Source: "cert", Target: "tls.crt", Mode: &ownMode},
		},
		Configs: []compose.ObjectRef{
			{Source: "app"},
			{Source: "nofile", Uid: "1000"},
			{Source: "abs", Target: "/etc/abs.conf", Mode: &defaultMode},
		},
		Environment: *environment,
		EnvFiles:    compose.EnvFiles{{Path: "app.env", Variables: map[string]string{"Keys": "file"}}},
		Labels:      compose.Labels{Map: map[string]string{"Keys": "label", "Map": "label"}},
		Volumes: compose.ServiceVolumes{
			Volumes: []*compose.ServiceVolume{data, cache},
			Map:     map[string]*compose.ServiceVolume{data.Target: data, cache.Target: cache},
		},
		Networks: compose.NewServiceNetworks(
			&compose.ServiceNetwork{Alias: "frontend", ResolvedName: "edge"},
			&compose.ServiceNetwork{Alias: "backend", ResolvedName: "stack_backend", Aliases: []string{"api"}},
		),
		Ports: compose.ServicePorts{Ports: []compose.ServicePort{
			{Published: 9090, Target: 90, Protocol: "tcp", Mode: "ingress"},
			{Published: 8080, Target: 80, Protocol: "tcp", Mode: "ingress"},
		}},
		InitJobs: []compose.InitJob{{
			Name: "prepare", Image: "alpine", Command: []string{"prepare"},
			Environment: *jobEnvironment,
			Networks:    compose.NewServiceNetworks(&compose.ServiceNetwork{Alias: "backend"}),
			Secrets: []compose.ObjectRef{
				{Source: "token", Target: "/run/secrets/token"},
				{Source: "cert", Target: "tls.crt"},
				{Source: "key"},
			},
			Configs: []compose.ObjectRef{
				{Source: "app", Target: "/run/secrets/app"},
				{Source: "nofile", Uid: "1000", Mode: &ownMode},
				{Source: "abs", Target: "/etc/abs.conf", Uid: "0", Mode: &defaultMode},
			},
		}},
	}
	other := compose.Service{Name: "another", Image: "busybox", Command: compose.NewCommand([]string{"true"})}
	return compose.File{Path: "compose.yaml", Digest: "source-digest", Compose: compose.Compose{
		Services: compose.Services{service, other},
		Networks: map[string]compose.Network{"frontend": {Name: "edge"}, "backend": {}},
		Volumes:  compose.Volumes{"data": {Name: "data"}, "cache": {Name: "cache"}},
	}}, nil
}
