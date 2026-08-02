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

[`../../bin/import_sched_public_pr`](../../bin/import_sched_public_pr) lives in the monorepo next to the mirror sync tooling. It rewrites patch hunk content for `*.go`, `go.mod`, and `*.md`, refuses binary or non-allowlisted path-bearing patches, and then runs `git am` in the target canonical worktree. The same helper supports companion CLI mirrors with `--tool jmap`, `--tool specd`, `--tool xmpp`, or `--tool voice`.

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
bin/sync_go_github
```

The sync treats `./go/sched` as canonical source when run from the monorepo root, clones or reuses the public mirror checkout under `$SUPER_TMP_DIR/sched-public-mirror-checkout` or `./tmp/sched-public-mirror-checkout` via the SSH remote `git@github.com:chatinfra/sched.git`, refuses dirty canonical or mirror state, requires mirror `HEAD` to match its fetched upstream exactly, copies only the scheduler subtree into the public mirror checkout, commits generated changes, and pushes the mirror branch. Use `bin/sync_go_github --tool jmap`, `bin/sync_go_github --tool specd`, `bin/sync_go_github --tool xmpp`, or `bin/sync_go_github --tool voice` to publish the companion module-root mirrors from `./go/<tool>` to `git@github.com:chatinfra/<tool>.git` with matching module-path transforms.

## Bootstrapping a new mirror

`bin/sync_go_github` only *maintains* an existing mirror: it requires the mirror checkout to be on a branch with a configured upstream whose `HEAD` it already matches. A freshly created, empty GitHub repository has neither, so the first publication of a new tool mirror is a one-time manual bootstrap. After it, `bin/sync_go_github --tool <tool>` works normally.

To bootstrap `chatinfra/<tool>` (replace `<tool>` with `jmap`, `specd`, or `xmpp`):

```sh
# 1. Create the public mirror repository.
gh repo create "chatinfra/<tool>" --public \
  --description "Public mirror of the ChatInfra <tool> CLI"

# 2. Clone it and seed an initial commit so a tracking branch exists.
git clone "git@github.com:chatinfra/<tool>.git" "tmp/<tool>-public-mirror-checkout"
cd "tmp/<tool>-public-mirror-checkout"
git checkout -b main
git commit --allow-empty -m "chore: initialize public mirror"
git push -u origin main
cd -

# 3. Run the normal sync against that checkout to publish the transformed source.
bin/sync_go_github --tool <tool> "tmp/<tool>-public-mirror-checkout"
```

Step 2's empty commit only exists to give the mirror a `main` branch with an upstream; step 3 then publishes the real, module-path-transformed source as the next commit. Verify the result by cloning the public mirror over `https://` and running `go build -trimpath ./cmd/<tool>` — it must build standalone, since OpenCode host launchers clone and build the mirror with no monorepo context.
