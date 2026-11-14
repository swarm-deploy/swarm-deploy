# Docker CLI differential tests

Run `make test-compatibility` from the repository root, or
`go -C tools/compatibility test ./...`.

This separate Go module pins the independent Docker CLI loader and converter to
v28.3.3. Its CLI command/trust dependencies are test tooling only; the application
module does not import the CLI loader/converter or add a Docker CLI executable.
The tests require no daemon or registry. CI and `make check` run them.

See `docs/native-deployment-compatibility.md` for intentional differences and the
real-Swarm scenarios.
