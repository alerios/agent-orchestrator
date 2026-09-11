# Building and running the local patches

How to run these branches — via `npm run dev`, or as a `.deb` that replaces
the officially installed app — and how to submit one upstream once it's
proven itself. Moved here from a per-project repo that had nothing to do with
these patches; see `README.md` in this directory for what each branch fixes.

## Running with `npm run dev`

```bash
export PATH="$HOME/go-sdk/bin:$PATH"
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_PORT npm run dev
```

Isolated data by default (port 3002, `~/.ao/dev/`) — see the `ao-desktop-dev`
skill for real-data mode and other options. Fast, no sudo, doesn't touch the
installed app.

## Prerequisites for a `.deb` build

| Tool | Version / notes |
|---|---|
| Go | `backend/go.mod` requires **1.25.7+**; the toolchain auto-fetches the exact version it wants. |
| Node | v20.20.0 (via nvm) works despite `EBADENGINE` warnings wanting ≥22 — the build bundles its own Node 22 runtime for ACP. |
| bison | `sudo apt-get install -y bison` — packaging **builds tmux from source**; without it the build fails at `configure: error: "yacc not found"`. |

npm deps must be installed in **both** `frontend/` and `packages/product-ui/`
(the latter is easy to miss — without it `npm run typecheck` reports dozens of
phantom errors from that package).

## Building a `.deb`

```bash
git checkout local/integration   # all 5 patches, or check out any single one to build just that
./docs/local-patches/build-local-deb.sh
```

Why a script: `frontend/package.json`'s version is stale by design — see
[Gotchas](README.md#gotchas) in the index — and upstream's own
`.github/workflows/build-artifacts.yml` handles this by stamping a
conductor-supplied version into `package.json` for the duration of one CI
build, never committing it. This script does the identical stamp-then-revert,
deriving the version from `git describe --tags` instead of a conductor. It
refuses to run with uncommitted changes to `package.json`, and reverts the
stamp on exit even if the build fails.

```
Usage: ./build-local-deb.sh [agent-orchestrator-checkout] [version-suffix]
  checkout defaults to the current repo root
  suffix   defaults to "local"
```

Internally this is `npm run make -- --targets=@electron-forge/maker-deb`. Use
`npm run make`, **not** `npx electron-forge make` directly — the latter skips
the `premake` hook that builds the daemon, tmux, the browser runtime, and the
ACP runtime, and packaging then fails with `ENOENT: ... lstat 'tmux'`.

Artifact lands at (the script prints the exact path):

```
frontend/out/make/deb/x64/agent-orchestrator_<version>_amd64.deb   (~145 MB)
```

## Installing

Close any running AO windows first, then:

```bash
sudo dpkg -i frontend/out/make/deb/x64/agent-orchestrator_<version>_amd64.deb
```

Replaces the official install in `/usr/lib/agent-orchestrator/`, keeps the
normal app-launcher icon, and uses your **real** `~/.ao` data — same projects
and sessions as before.

Verify it's actually the patched build:

```bash
dpkg -l | grep agent-orchestrator
strings /usr/lib/agent-orchestrator/resources/daemon/ao | grep -c orchestratorRulesFile   # >0 if that patch is in this build
```

Revert to stock AO any time by downloading an official release and
`dpkg -i` it — `~/.ao` data is untouched by either direction.

## Keeping branches current

Upstream moves fast (often tens of merged PRs a day):

```bash
git checkout main && git pull
for b in feat/orchestrator-rules-file fix/opencode-model-refresh \
         fix/approval-request-resolution fix/workspace-orchestrator-verification \
         fix/model-dependent-session-mode local/patch-index; do
  git checkout "$b" && git rebase main || { echo "CONFLICT on $b"; break; }
done

# Rebuild local/integration fresh rather than rebasing it — it's just five
# cherry-picks, simpler to recreate than to re-resolve the same conflicts twice.
git checkout -B local/integration main
for c in feat/orchestrator-rules-file fix/workspace-orchestrator-verification \
         fix/opencode-model-refresh fix/approval-request-resolution \
         fix/model-dependent-session-mode; do
  git cherry-pick "$c" || { echo "CONFLICT cherry-picking $c"; break; }
done
```

Re-verify a branch stands alone before submitting it:

```bash
export PATH="$HOME/go-sdk/bin:$PATH"
git checkout <branch>
(cd backend && go build ./... && go test ./internal/<touched-pkg>/...)
(cd frontend && npm run typecheck && \
   npx vitest run --config vite.renderer.config.ts <touched-test-files>)
```

If a patch touches the API surface, regenerate before building:
`npm run api` from the repo root, and commit `openapi.yaml` plus
`frontend/src/api/schema.ts` alongside the Go change.

## Submitting one upstream

```bash
git push fork <branch>
gh pr create --repo Untrivial-ai/agent-orchestrator --base main --head alerios:<branch>
```

`fork` is a personal fork (`alerios/agent-orchestrator`) — write access to
`Untrivial-ai/agent-orchestrator` itself is read-only, so a fork is the
standard vehicle for opening a PR, not a sign of permanent divergence. Never
push or PR `local/integration` or `local/patch-index` — submit the individual
patch branches.

Per the repo's own `AGENTS.md`: one issue per PR, conventional commit
subjects, and run the CI jobs locally first (this directory's per-branch
verification above is a subset of that).
