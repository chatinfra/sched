# Contributing to sched

Thanks for improving `sched`.

## Repository model

- Public mirror: <https://github.com/chatinfra/sched.git>
- Canonical source: `go/sched/` inside the ChatInfra monorepo

The public mirror exists for inspection, forks, local host edits, and pull requests. It is downstream of the monorepo. Maintainers import accepted public PRs into canonical `go/sched` first, then synchronize the mirror.

The mirror sync rewrites published Go and Markdown module-path references so public-facing examples use the mirror module path, for example:

```sh
go install github.com/chatinfra/sched/cmd/sched@latest
```

## Fork-and-PR flow

```sh
git clone git@github.com:<you>/sched.git
cd sched
git checkout -b my-sched-change
go test ./...
git push -u origin my-sched-change
```

Then open a pull request against `chatinfra/sched`. Include any behavior, compatibility, or systemd implications in the PR description.

## Host-local edits

OpenCode hosts keep an editable mirror checkout at `/data/opencode/src/sched` and run `/data/opencode/bin/sched` as the stable scheduler launcher. Use this for diagnostics or emergency local patches:

```sh
sudo -u opencode git -C /data/opencode/src/sched status
sudo -u opencode /data/opencode/bin/sched --help
```

Reconfigure preserves dirty host checkouts and logs a warning instead of resetting local work. To return to mirror updates, commit/stash/revert local edits, then re-run reconfigure so the clean checkout can fast-forward.

## Maintainer import and mirror sync

Maintainers import accepted public changes into canonical `go/sched`, preserving the monorepo as source of truth. For reviewed public mirror commits, generate an `mbox` patch and apply it with the monorepo helper so patch hunks are reverse-transformed back to the canonical module path:

```sh
git -C /path/to/chatinfra-sched-mirror format-patch -1 --stdout <accepted-commit> > /path/to/pr.patch
../../bin/import_sched_public_pr /path/to/pr.patch /path/to/monorepo/go/sched
```

[`../../bin/import_sched_public_pr`](../../bin/import_sched_public_pr) lives in the monorepo next to the mirror sync tooling. It rewrites patch hunk content for `*.go`, `go.mod`, and `*.md`, refuses binary or non-allowlisted path-bearing patches, and then runs `git am` in the target canonical worktree.

For a one-off text-only patch that touches only `*.go`, `go.mod`, and `*.md`, the equivalent `git format-patch | sed | git am` flow is:

```sh
canonical_module='super/go'/'sched'
public_module_regex='github[.]com/chatinfra'/'sched'
git -C /path/to/chatinfra-sched-mirror format-patch -1 --stdout <accepted-commit> \
  | sed -E "/^[ +-]/ s#${public_module_regex}#${canonical_module}#g" \
  | git -C /path/to/monorepo/go/sched am -
```

Prefer the helper for normal imports; it validates patch shape before applying. After the monorepo change lands, run the mirror sync tooling from the monorepo to update the public repository:

```sh
bin/sync_sched_public_mirror /path/to/chatinfra-sched-mirror
```

The sync refuses dirty canonical `go/sched` state, stages temporary data under `$SUPER_TMP_DIR` or `./tmp`, and copies only the scheduler subtree into the public mirror checkout.
