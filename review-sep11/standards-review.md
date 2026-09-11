# Standards review — September 11 stack and verified fixes

Review scope: all six actual-base diffs from `review-stack.json`. Read-only source review with bounded external-overlay regressions; no shared-source edits, pushes, PR comments, or full-suite execution. Broad CI/native validation belongs to the parent task. Repository rules take precedence over the smell baseline, and tooling-enforced formatting/typecheck/generated drift issues are excluded from the findings below.

## Sources

- `AGENTS.md`, especially lines 75–83 (surgical changes, package boundaries, error envelopes, Context first, real-use helpers, behavior/error tests), 87–99 (loopback, durable facts, generation/lifetime safety, append-only migrations, generated SQL, trigger-owned CDC, state under ~/.ao).
- `docs/architecture.md`, Core Architectural Principles, Session Interface Handoff, Persistence and CDC, and Load-Bearing Rules.
- `docs/adr/0003-persistent-chat-provider-host.md`, especially lines 27–49: exclusive host attachment, SQLite controller-generation projection fence, detach versus explicit termination, and no fabricated exit on detach.
- `CONTEXT.md`, canonical listener/control vocabulary. No additional scoped AGENTS.md files were found in backend/frontend.

## Final disposition

All three demonstrated correctness findings below are fixed in the final reviewed heads: queue `16a908352807681007f6d96d13d85f479353e4f2`, inline `0c9cbaa74d863431f7e38dff7d5a8fe45e341ce1`, and downstream steer `84420c7f10205ebc2d2a800b83ccc932a7d334f7`. The xterm/text/attachments heads are unchanged from the original manifest. No unresolved documented-standard violation was established. The two smell observations are optional, nonblocking judgments; the accepted scope and size are not findings.

## Fixed correctness findings / standards-relevant boundary coverage

### Fixed P1 — Definitive queued-edit refusals permanently lock the editor (#5109)

Pinned `queue` head: `frontend/src/renderer/components/chat/ChatWorkspace.tsx`, handleComposerSend around lines 774–791; cancel/begin/delete guards around 752–757, 800–818. Equivalent positions in pinned final `steer` head are 789–803 and 764–833. `ChatComposer.tsx` recovery lock guards cover cancel, Escape, attachments, and editor input.

The changed code durably writes:

```ts
saving: true,
clientMessageId: currentEdit.clientMessageId ?? crypto.randomUUID(),
...
await onEditQueuedTurn(...);
```

There is no rejection branch to retire that handle. Every later edit/cancel/replacement path treats its presence as unresolved forever. Concrete case: open queued editor at revision N, the daemon changes or dispatches the queued turn, then save. The response is `CHAT_QUEUED_EDIT_CONFLICT` or `CHAT_TURN_NOT_QUEUED`, but retries preserve the same stale revision and Cancel/Escape remain disabled. This also survives app restart. The accepted queued-edit receipt is atomically checked before current-turn validation, so definitive *post-receipt-lookup* rejections can unlock the editable draft without losing an accepted edit; key/payload conflict needs its own classification and ambiguous transport must remain locked.

`AGENTS.md:83` says tests should cover “the user-visible behavior and boundary being changed ... validation/missing args, daemon error envelopes”. The new cancellation tests covered ambiguous outcomes, but not the definitive refusal path now made destructive to usability. This is a correctness finding with a concrete missing error-boundary test, not a generic complaint about test quantity. Parent independently reproduced it with red tests. Verified fix at queue `16a908352`: the hook preserves the API envelope; payload/key collisions have the separate `CHAT_QUEUED_EDIT_IDEMPOTENCY_CONFLICT` code; only definitive codes produced after receipt lookup clear the retained key, including recovered requests. Unknown transport and reused-key outcomes retain it. The draft update keeps its revision fence. The preceding description concerns the original pinned head.

### Fixed P2 — Composer clone overwrites the queue recovery disabled state (#5109)

`ChatWorkspace.tsx` gives `QueuedMessageDock` `disabled={Boolean(queueEdit?.clientMessageId)}`, but `ChatComposer.tsx`'s `queuedDockWithSteer` clone overwrites it:

```ts
{ canSteerNext, steerNextRequest, disabled: submitting }
```

After an uncertain save settles, `submitting` becomes false while the durable handle remains. Dock actions look enabled although cancel/start-edit handlers now silently return; promote/reorder handlers are still callable. Preserve the child's disabled flag when adding the composer submission flag. This is a UI correctness observation, not a standalone architectural-rule violation. Verified fix at queue `16a908352`: `disabled: submitting || queuedDock.props.disabled` preserves the unresolved receipt lock.

### Fixed P1 — Failed shutdown recovery invalidates a surviving provider’s credentials (#5110)

Pinned `backend/internal/service/chat/history.go:986–997` skipped source termination whenever its event-stream state was already stopped, and ignored termination errors if that state became stopped:

```go
if source.State() != ports.ChatControllerStopped {
    closeErr := source.closeForBranchHandoff(recoveryCtx)
    if source.State() != ports.ChatControllerStopped {
        if closeErr == nil {
            closeErr = errors.New("source controller did not stop")
        }
        return fmt.Errorf(
            "confirm source stopped before failed native edit recovery: %w", closeErr)
    }
}
```

Concrete failure: `backend/internal/adapters/chatdriver/codexappserver/driver.go:449–453` closes the transport before authenticated host Shutdown. A transient shutdown failure therefore leaves a stopped controller and a live provider. Recovery proceeded to `prepareBranchControllerEnv`, rotated the durable browser verifier, and resumed the source. Codex’s `Reconnected` Resume path at `driver.go:331–341` reuses that provider without applying the new environment. Its original browser capability then fails authentication. The original host does not have to reattach for the credential invalidation itself to occur.

Rules: `docs/architecture.md:352` requires source stop and shutdown confirmation before handoff; `docs/adr/0003-persistent-chat-provider-host.md:41–49` separates projection generation from host lifetime and transport detach from explicit termination. A stopped projection is not confirmation of host shutdown. `AGENTS.md:83` requires coverage of the changed error boundary.

Demonstrated externally with actual SQLite/browser Authority and the real Codex termination ordering modeled at the service seam: pinned source rotates twice, reattaches once, publishes a replacement, and rejects the surviving provider’s original capability. A separate real `persistenthost.Run` subprocess/TCP test shows that a failed Shutdown can leave the exact same provider PID alive and reconnectable.

Verified fix at inline `0c9cbaa74`: `restoreClosedSourceController` always calls `closeForBranchHandoff` and propagates the cached Terminate error before credential preparation. Existing `Controller.Terminate` waits for the stopped signal and preserves the termination result, so this covers both the already-stopped and stopping cases. The added `TestFailedBranchShutdownPreservesSurvivingHostCredentials` asserts no rotation, reattachment, or replacement, and validates the retained capability against the real Authority. Parent reports its focused regression green; the independent overlay also passes against this final source.

No other direct hard architecture/package/lifetime standard violations were established.

## Smell baseline — optional judgments, not hard violations

### Possible Repeated Switches / Duplicated Code (#5110)

`frontend/src/renderer/lib/chat-drafts.ts:254–264, 305–324, 335–354, 408–425, 449–462` repeats the same `kind === "composer"` branch to choose a token/receipt slot. For example:

```ts
if (kind === "composer") {
  if (runtime.composerToken !== token) return;
  runtime.composerToken = undefined;
  clearDraftMutationReceipt(runtime, "composer", false);
} else {
  if (runtime.inlineEditToken !== token) return;
  runtime.inlineEditToken = undefined;
  clearDraftMutationReceipt(runtime, "inline-edit", false);
}
```

The finish functions repeat the same ownership/sequence/emit shape at 370–406. A small typed mutation-slot helper could keep this transition logic in one place while preserving number-versus-string revision typing. This is optional; no class hierarchy or broad draft rewrite is warranted. AGENTS.md's surgical-change and real-call-site rules override any preference for a larger abstraction.

### Possible Data Clumps (#5110)

`ChatWorkspace.tsx` repeats the new edit recovery props through Timeline, TurnGroup, TimelineItem and HumanMessage; representative hunk at 3005–3012:

```ts
onCancelMessageEdit: () => void;
onAbandonEditRecovery?: () => void;
onSubmitMessageEdit: (text: string) => Promise<void>;
editPending?: boolean;
editSendBlocked?: boolean;
editRecoveryLabel?: string;
editBusy?: boolean;
editError?: string;
```

These describe one editor control state and travel together. A named, typed editor-controls value would reduce the repeated signatures and prop wiring. This is a low-priority maintainability judgment, not a blocker or a request to collapse the existing thin renderer boundaries.

## Important negative checks

- #5105: disposal is queued after the viewport task in both production and development. AO listeners are removed first. No async mount-owned data revival is introduced by the changed hunk.
- #5106: ordinary composer receipts retain exact revision/key/text; ordinary definitive first-send rejection is distinguished from an uncertain retry; local persistence and attachment-pending boundary risks remain separate; scope leases fence obsolete session incarnations. Confirmed interface-switch discard captures only the work approved at that boundary and waits for the matching durable transition.
- #5107: staged descriptors contain paths rather than raw bytes; missing or failed staging does not yield a chip that looks sendable. Native payload reconstruction preserves chip order and old-generation staging cannot revive discarded attachments. Restored previews use the session attachment route.
- #5109: queue update plus acceptance hash commit transactionally. Accepted receipt lookup precedes current mode/controller/turn checks, preserving successful replay after dispatch. Key mismatch fails closed. Completed editor metadata purge does not delete worktree bytes. The originally found refusal/disabled-state defects are fixed as described above.
- #5110: `provider_work_started=0` reservations can be resumed; the store atomically claims provider work against the current controller generation before Fork/Start/Resume. Existing reservations migrate to 1, conservatively. Provider-started reservations cannot redispatch. Completed-turn repair requires the same client id, completed turn, user message, and matching replaced source turn. Missing-turn rejected replay retains both `ErrEditTurnInvalid` and `domain.ErrNoConversationTurn`, preserving the 404 mapping. Final accepted/rejected receipt writes detach from HTTP cancellation. Host Shutdown now waits for descriptor/lock release before reporting success, and Codex Terminate propagates errors.
- #5111: steer reservations precede provider I/O; success persists activity plus receipt in one transaction; generic errors remain uncertain; rejected receipt kinds reconstruct typed errors. `recoverOnly` bypasses provider readiness/content processing and only reads the durable receipt, including image guidance after the controller disappears.
- API schema/TypeScript changes accompany DTO changes; existing migrations are not modified; new store operations use existing transaction/trigger CDC behavior. Adapters remain leaves. No new remote listener/auth bypass or OS-default AO app-state path was added.
- Migration ordering 132 before 131 is not itself a startup failure: production migration uses `goose.WithAllowMissing()` at db.go:254. The original pinned migration ledger omitted 132; parent registered it in final inline commit `46e909ca1`. This tooling-enforced issue is excluded from the smell findings.

## Bounded regression evidence

All artifacts are under `/tmp/pr4463-review-fixes-sep11/shutdown-overlay/`; Go overlays use canonical `/private/tmp/...` source paths and do not change shared worktrees.

- `pinned-red.log`: FAIL, pinned `44bfe02b2` history overlaid into the current package; `rotations=2`, `reattachments=1`, `replacementPublished=true`, original capability unauthorized.
- `fixed-green.log`: PASS against final guarded source; `rotations=1`, `reattachments=0`, `replacementPublished=false`.
- `control.log`: PASS against the independently proposed external guard.
- `host.log`: PASS with a real provider helper process; after injected transient TCP shutdown failure the same provider PID reconnects. This test establishes the real-host premise independently of the service fake. It uses loopback TCP in the persistenthost package’s existing subprocess/integration pattern.
- Commands: `go test -overlay=<pinned-overlay.json|overlay.json|control-overlay.json> ./internal/service/chat -run '^TestSep11FailedShutdownMustNotInvalidateSurvivingHostBrowserToken$' -count=1 -v`; `go test -overlay=host-overlay.json ./internal/adapters/chatdriver/persistenthost -run '^TestSep11FailedShutdownCanLeaveSameProviderReattachable$' -count=1 -v` (overlay files are in the artifact directory).

## Final actual-base comparisons

The original full diffs below were reviewed first; the changed refusal/disabled, migration-ledger, and shutdown-recovery hunks were then rechecked in their final commits. Final local heads match those reported published by the parent; remote CI verification belongs to the parent task.

- #5105: `8343bcc089982e202f0fbc37ff43aa09de3110d4...fabbcd6ada7f68e244a07b423f6401d6608e43b2`.
- #5106: `fabbcd6ada7f68e244a07b423f6401d6608e43b2...ff2440b5df1e7451b28a38c6d86dcb83a9e95898`.
- #5107: `ff2440b5df1e7451b28a38c6d86dcb83a9e95898...92bf1117f67008d10d3de1779bcf2643a7420ebf`.
- #5109: `92bf1117f67008d10d3de1779bcf2643a7420ebf...16a908352807681007f6d96d13d85f479353e4f2`.
- #5110: `16a908352807681007f6d96d13d85f479353e4f2...0c9cbaa74d863431f7e38dff7d5a8fe45e341ce1`.
- #5111: `0c9cbaa74d863431f7e38dff7d5a8fe45e341ce1...84420c7f10205ebc2d2a800b83ccc932a7d334f7`.

## Original pinned comparisons (audit trail)

- #5105 (xterm), `/tmp/pr4463-split/xterm`: `git diff 8343bcc089982e202f0fbc37ff43aa09de3110d4...fabbcd6ada7f68e244a07b423f6401d6608e43b2`.
- #5106 (text), `/tmp/pr4463-split/text`: `git diff fabbcd6ada7f68e244a07b423f6401d6608e43b2...ff2440b5df1e7451b28a38c6d86dcb83a9e95898`.
- #5107 (attachments), `/tmp/pr4463-split/attachments`: `git diff ff2440b5df1e7451b28a38c6d86dcb83a9e95898...92bf1117f67008d10d3de1779bcf2643a7420ebf`.
- #5109 (queue), `/tmp/pr4463-split/queue`: `git diff 92bf1117f67008d10d3de1779bcf2643a7420ebf...aaf6df35e571d4c231d4ff0230049c9caed9ec58`.
- #5110 (inline), `/tmp/pr4463-split/inline`: `git diff aaf6df35e571d4c231d4ff0230049c9caed9ec58...44bfe02b25352942ca473a3dab9f888a9f897827`.
- #5111 (steer), `/tmp/pr4463-split/steer`: `git diff 44bfe02b25352942ca473a3dab9f888a9f897827...633b27ca99af5b10517bf461dd9a447959b73504`.

## Parent validation addendum

The only changes after the reviewed implementation heads were regression-test corrections: allow the stopped-source watcher to remove its registry entry; avoid putting expensive fixture migration under a five-second context; and require Escape to preserve an unavailable queue target’s unresolved receipt. The shutdown regression passed ten consecutive runs under `go test -race -timeout=2m`, and the 112-test workspace suite passed. Current final heads are in `review-stack.json`; the implementation changes inspected by the independent reviewer are unchanged.

Final tooling-only update: #5110 changed the shutdown fixture’s `h.fakeConversation.Close()` selector to its equivalent promoted `h.Close()` call, as required by pinned staticcheck QF1008. The complete pinned linter then passed with zero issues. No implementation behavior changed; the final heads are recorded in the manifest.
