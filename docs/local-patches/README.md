# Local patches to Agent Orchestrator

Local, unsubmitted work against this checkout, kept as one branch per patch so
each can be verified, rebased, and eventually opened as its own upstream PR
without dragging the others along.

**This branch (`local/patch-index`) is local-only bookkeeping. Never merge it
into a patch branch and never include it in a PR.** It exists so the index
itself is version-controlled next to the code it describes.

Nothing here has been submitted upstream. No issues have been filed.

## Patches

| Branch | Fixes | Verified | Upstream-ready |
|---|---|---|---|
| `feat/orchestrator-rules-file` | orchestrator rules can live in a git-tracked file; adds a manual Restart action | build + 152 FE + BE tests | yes, but see note |
| `fix/opencode-model-refresh` | newly added local models missing from opencode's picker | build + 14 FE + its BE test | yes |
| `fix/approval-request-resolution` | percent-encoded request ids never matched, so approvals could not be answered | build + BE + 33 FE tests | yes |
| `fix/workspace-orchestrator-verification` | replacing a workspace project's orchestrator always failed verification | build + BE tests | yes |
| `fix/model-dependent-session-mode` | selecting a model that does not offer AO's mapped approval mode failed the turn | build + BE tests | yes |

All five are based directly on `origin/main` and were confirmed to build and
pass their own tests **independently**, which is what makes them separately
submittable.

---

### `feat/orchestrator-rules-file`

Adds `orchestratorRulesFile` to project config, mirroring the existing
`agentRulesFile`: a repo-relative file whose contents are read fresh on every
spawn and restore. Stock AO offers a file option for `agentRules` (workers) but
not for `orchestratorRules`, so orchestrator rules could only ever live as
opaque text in each machine's `ao.db` — impossible to commit, review, or share.

Also adds an explicit **Restart** action for an orchestrator session (in the
session's actions menu and on the board topbar), because editing rules and
*resuming* cannot work: chat sessions resume through
`@agentclientprotocol/claude-agent-acp`, whose `getOrCreateSession` returns
early on a fingerprint match, and `computeSessionFingerprint` hashes only `cwd`
and `mcpServers` — never the system prompt. A resumed conversation therefore
keeps the prompt it was created with, so a fresh session is the only way to pick
up a rules change. The action reuses AO's existing
`restartProjectOrchestrator` clean-spawn path rather than adding an endpoint.

Both halves are needed together to be useful, but they are arguably two PRs
(config surface vs. UI affordance). Consider splitting if a reviewer asks.
The generalized rules loader renames `projectRulesConfig`'s fields from
`Agent*` to role-neutral `Rules`/`RulesFile`, which is a small rename in a
shared helper — worth calling out in the PR description.

Depends on nothing. **`fix/workspace-orchestrator-verification` is effectively a
prerequisite for the Restart action to work on workspace projects** (see below);
if only one of the two lands, Restart stays broken for workspaces.

### `fix/opencode-model-refresh`

Cached opencode model scopes were not revalidated during catalog warm-up, so
models added to a local provider (e.g. a newly pulled Ollama model) never
appeared in the picker until the cache aged out. Adds opencode to the
revalidated scopes and surfaces a refresh affordance; includes the i18n keys
for all eight shipped locales.

### `fix/approval-request-resolution`

An ACP request id embeds literal colons (`acp-request:<hash>:1`). Any client
that percent-encodes its path correctly sent `acp-request%3A<hash>%3A1`, which
never matched the unescaped id AO generated, so *every* approval or
structured-input answer reported the request as no longer pending regardless of
how fast the user answered. Unescapes the path param before comparison.

Second, narrower half: on a genuinely rejected resolve the UI left a dead
approval card the user could not get past. Now refetches on error, and retries
`CHAT_REQUEST_NOT_PENDING` up to three times, since a real user-clicked
decision can land in the brief window where the daemon's live tracking
disagrees with its durable pending read model. The retry is scoped to that one
error code so a superseded or invalid decision still fails fast.

The backend unescaping and the frontend retry could be two PRs. They are kept
together because they address one reported symptom — an unanswerable approval
card — and the retry is only reachable because of that error code.

### `fix/workspace-orchestrator-verification`

Replacing a workspace project's orchestrator always failed verification. The
check compared the new session's branch to a single fixed generated name, but a
workspace project shares one branch name across its root and every child repo,
so gitworktree disambiguates with a numeric suffix whenever the name is already
taken — including by the still-registered worktree of the orchestrator being
replaced. The replacement landed on a legitimately suffixed branch and was
rejected.

Skips the fixed-name check for workspace projects (no single expected name
exists) and checks the per-session generated name for single-repo projects.
Removes the now-unused `DefaultOrchestratorBranch`.

Found by using `feat/orchestrator-rules-file`'s Restart action on a workspace
project, so the two are related in practice even though they touch different
code.

### `fix/model-dependent-session-mode`

Switching a chat session to a model that does not advertise AO's mapped mode
failed the turn outright. Claude Code only offers the `auto` classifier mode
for models whose SDK reports classifier support, so selecting a model without
it (Haiku) left the static AO-vocabulary mapping asking for a mode the live
session never listed.

Now checks the agent's advertised choices before `session/set_mode` and reports
`ErrACPSetterUnsupported` when absent — which Start and Resume already
tolerate, since an initial mode can legitimately arrive via launch-time flags.
Also skips `set_mode` entirely when the requested mode already equals the
current one, so an unchanged setting cannot fail a turn.

---

## Working with these branches

Rebase all of them after upstream moves (it moves fast — often tens of merged
PRs a day):

```bash
git checkout main && git pull
for b in feat/orchestrator-rules-file fix/opencode-model-refresh \
         fix/approval-request-resolution fix/workspace-orchestrator-verification \
         fix/model-dependent-session-mode local/patch-index; do
  git checkout "$b" && git rebase main || { echo "CONFLICT on $b"; break; }
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

Submitting one upstream (the remote still uses the org's old name,
`ComposioHQ/agent-orchestrator`, which GitHub redirects to
`Untrivial-ai/agent-orchestrator` — same repo):

```bash
git push -u origin <branch>
gh pr create --repo Untrivial-ai/agent-orchestrator --base main --head <branch>
```

Per the repo's own `AGENTS.md`: one issue per PR, conventional commit subjects,
and run the CI jobs locally first. If a patch touches the API surface,
regenerate with `npm run api` and commit `openapi.yaml` plus
`frontend/src/api/schema.ts` alongside the Go change.

## Not upstreamable as-is

`docs/opencode-local-models.md` (on this branch) documents pointing opencode
sessions at local Ollama models. It references this machine's layout — a
sibling `~/Tech/ai-magic/ai-local-models` repo, a relative path that escapes
the repo root, and "19 skills on the reference machine" — so it would need
de-localizing before it could be offered upstream. Kept here as local
reference material.

## Gotchas

**`internal/service/agent` fails ~21 tests in this environment regardless of
any patch.** They are Codex credential/secure-file tests reporting "codex
directory has a writable ancestor" and fail identically on unmodified
`origin/main`. Verified by comparison, not assumption — do not treat them as
caused by a patch. The full frontend suite is also flaky in aggregate (the
failure count varies run to run and was *higher* on unpatched code); per-file
runs are clean.

**`frontend/package.json`'s version is permanently stale** (reads `0.10.3` even
on current `main`). Real versions come from git tags — use
`git describe --tags --abbrev=0 main`.

**`archive/tangled-merge-20260911`** preserves the earlier state where
`feat/orchestrator-rules-file` had `fix/opencode-model-refresh` merged into it
via a merge commit. Both patches were recovered onto clean single-commit
branches; the archive ref exists only as a safety net and can be deleted once
that is no longer wanted.

**Durable backup** of the pre-split state (the then-uncommitted work as a
patch file, plus a bundle of the branches) is in
`~/ao-patch-backup-20260911-103017/`.

## Building, running, and submitting

See [`BUILDING.md`](BUILDING.md) in this directory: running with `npm run dev`,
building a `.deb`, keeping branches current against fast-moving upstream, and
opening a PR from the personal fork (`alerios/agent-orchestrator`) once a
patch is ready.
