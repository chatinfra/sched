# ChatInfra sched

`sched` is the local OpenCode-host scheduler for ChatInfra command schedules. It stores schedule state on the OpenCode host, reconciles user-systemd timers, runs scheduled OpenCode commands, and records scheduler run history without calling the ChatInfra API.

## Public mirror

The public repository is <https://github.com/chatinfra/sched.git>. Its root is a mirror of this monorepo's canonical `go/sched/` subtree, so public checkouts contain `go.mod`, `cmd/sched`, `internal`, tests, and these scheduler docs directly at repository root.

`go/sched` in the ChatInfra monorepo remains canonical. Maintainers import accepted public changes back into the monorepo first, then update the public mirror from the canonical subtree. The mirror sync rewrites published Go and Markdown module-path references so mirror checkouts use the public module path in examples such as `github.com/chatinfra/sched/cmd/sched`.

## Build and test

```sh
go test ./...
go build ./cmd/sched
```

The module currently declares Go 1.24 in `go.mod`. From a published mirror checkout, module-path installation uses the same public path shown after sync:

```sh
go install github.com/chatinfra/sched/cmd/sched@latest
```

## OpenCode host layout

Source-backed OpenCode hosts use these stable paths:

| Path | Purpose |
| ---- | ------- |
| `/data/opencode/src/sched` | Editable source checkout cloned from the public mirror |
| `/data/opencode/bin/sched` | Stable launcher used by ChatInfra API/reconfigure and rendered systemd units |
| `/data/opencode/.cache/sched` | Cached build output for the launcher |
| `/data/opencode/.cache/go-build` and `/data/opencode/.cache/go-mod` | OpenCode-owned Go build and module caches |

The launcher rebuilds `./cmd/sched` when the source hash changes or the cached binary is missing, then execs the cached binary with the original arguments. ChatInfra-controlled operations run the launcher as the `opencode` user so editable scheduler source is not executed as root.

For local host edits:

```sh
sudo -u opencode git -C /data/opencode/src/sched status
sudo -u opencode editor /data/opencode/src/sched/internal/...
sudo -u opencode /data/opencode/bin/sched --help
```

Installer and reconfigure flows preserve dirty `/data/opencode/src/sched` checkouts. Clean checkouts may be fast-forwarded from the configured mirror.

## Contribution workflow

1. Fork <https://github.com/chatinfra/sched.git>.
2. Clone your fork and create a topic branch.
3. Make changes, run `go test ./...`, and push the branch.
4. Open a pull request against the public mirror.

Accepted public changes are reviewed and imported into canonical `go/sched` in the ChatInfra monorepo before the public mirror is synchronized again. See [CONTRIBUTING.md](./CONTRIBUTING.md) for details.

