# BudgieBBS

BudgieBBS is a real-time forum/BBS server with a web client and an SSH TUI.
The core model is command/event driven: clients send commands, the server
validates them through one authority path, appends durable events, updates
derived views, and streams live updates to connected clients.

## Run Locally

Build the web bundle and server:

```sh
./scripts/build-web.sh
go build -o budgied ./cmd/budgied
```

Run the single-node SQLite shape:

```sh
./scripts/run-single-node.sh
```

By default this serves HTTP on `:8080` and the SSH TUI on `2222`. For a durable
single-host install, see [deployment-single-node.md](deployment-single-node.md).

## Canonical Docs

- [Protocol Definition](protocol-definition.md) - command/event envelope,
  transport ladder, REST surface, and command/event catalogs.
- [Architecture](doc/architecture.md) - the durable architecture principles and
  client/server model.
- [Roadmap](doc/roadmap.md) - current product, backend, and scale roadmap.
- [Single-node Deployment](deployment-single-node.md) - one-process SQLite
  deployment and launchd/systemd setup.
- [Multi-node Deployment](deployment-multi-node.md) - Postgres, runtime roles,
  observability, recovery, and cluster validation.
- [Internet-scale Milestones](milestones-internet-scale.md) - scale epic status,
  gate evidence, and remaining hardening work.
- [Internet-scale Write Design](design-internet-scale-writes.md) - partitioned
  command/event log design behind the scale epic.
- [Repo Maintenance](doc/repo-maintenance.md) - cleanup rules for docs, scripts,
  and tests.

## Operational Scripts

Common entrypoints:

- `./scripts/build-web.sh`
- `./scripts/run-single-node.sh`
- `./scripts/install-single-node-launchd.sh`
- `./scripts/cluster-smoke.sh`
- `./scripts/internet-scale-staging-gate.sh`
- `./scripts/commandlog-native-nats-gate.sh`
- `./scripts/commandlog-native-kafka-gate.sh`

Script behavior is pinned by [scripts/scripts_test.go](scripts/scripts_test.go).
Prefer updating the canonical docs and script tests together when a workflow
changes.

## Test Shortcuts

```sh
GOCACHE=/private/tmp/budgie-go-build go test ./...
./scripts/build-web.sh
```

Use `/opt/homebrew/bin/go` when the shell does not put Homebrew Go on `PATH`.
