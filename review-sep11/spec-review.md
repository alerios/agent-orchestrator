# Final independent spec/correctness review — 11 September 2026

**Final disposition: no remaining actionable spec/correctness findings.** The final test-only correction resolves the independently reproduced registry-cleanup race; the shutdown implementation remains unchanged. The sole finding from the first review, the definitive queued-edit refusal lock, is resolved by `16a908352807681007f6d96d13d85f479353e4f2` and propagated correctly through the stack. No missing/partial accepted requirements or unrequested behavior were identified. This is a review conclusion, not a claim that every possible lifecycle failure has been exhaustively tested.

Final pinned heads reviewed:

| PR | Head |
| --- | --- |
| #5105 | `fabbcd6ada7f68e244a07b423f6401d6608e43b2` |
| #5106 | `ff2440b5df1e7451b28a38c6d86dcb83a9e95898` |
| #5107 | `92bf1117f67008d10d3de1779bcf2643a7420ebf` |
| #5109 | `16a908352807681007f6d96d13d85f479353e4f2` |
| #5110 | `320cb92c3675ed5bdd083c974bd39cb78275f167` |
| #5111 | `8ff2e0dcab9b8a81e46a9e11dcd020a4f5b02f4d` |

All updated diff and log commands in `review-stack.json` were rerun. Final diff snapshots are in `spec-diffs-final/`. The final cumulative changes since the initial reviewed head comprise the queue correction, the missing migration-132 registry test entry, the separately reviewed shutdown guard/regression, and its test-only race correction. No unrelated implementation changes were introduced. The steer worktree remained clean.

## Final shutdown review — separate from the earlier queue re-review

The Standards reviewer subsequently demonstrated a failure that my earlier review did not identify: a persistent host can remain alive after its transport closes and the controller reports stopped. The old source-recovery path skipped its cached termination error, rotated browser credentials, and reattached the surviving host with an invalidated immutable token.

I separately reviewed commit `0c9cbaa74d863431f7e38dff7d5a8fe45e341ce1` and its propagation to `84420c7f10205ebc2d2a800b83ccc932a7d334f7`. The cumulative delta contains only `history.go` and `branch_shutdown_test.go`. `restoreClosedSourceController` now always calls `closeForBranchHandoff` and returns any termination error before credential rotation or Resume. `Controller.Terminate` returns its cached error and waits for stream closure on success, so a projected stopped state cannot bypass this guard. No additional production defect was identified in this correction.

The Standards review's red/control/fixed logs under `shutdown-overlay/` were inspected as supporting evidence; they were produced by that reviewer, not by this spec review. They show rotations dropping from 2 to 1, reattachments from 1 to 0, and no published replacement after the correction.

An independent run of the newly committed regression test exposed a test-only race:

```sh
cd /tmp/pr4463-split/steer/backend
go test ./internal/service/chat -run '^TestFailedBranchShutdownPreservesSurvivingHostCredentials$' -count=1 -v
```

Output `spec-final-shutdown-check.log` reports the desired `rotations=1` and `reattachments=0`, but fails because `Controller()` returns `ErrNoController`. This is legitimate: `Service.Start`'s watcher (`service.go:650-659`) deletes the stopped source after `EditMessage`'s deferred `AbortHandoff` releases the branch latch. The test's requirement that the source must still be registered is therefore timing-dependent. Reported to the parent for correction: allow either the same source or its removal, while forbidding replacement and checking the original credential remains valid.

### Final test-only correction

Separately reviewed `320cb92c3675ed5bdd083c974bd39cb78275f167`, propagated to final cumulative head `8ff2e0dcab9b8a81e46a9e11dcd020a4f5b02f4d`. The entire final delta is 7 added/3 removed lines in `branch_shutdown_test.go`; no production code changed.

The assertion now accepts `ErrNoController` from legitimate asynchronous watcher cleanup, rejects every other controller error, and still forbids a different registered controller, extra credential issuance, or reattachment. It continues to verify the surviving host's original browser token. Replacing the test's five-second deadline (which included migration setup) with `context.Background()` matches the surrounding harness; the test runner supplies an overall timeout.

The parent reported ten consecutive passes under `-race -timeout=2m`; I inspected the successful output in `host-shutdown-refusal-final.log`. Those repeated runs were performed by the parent. My independent failed run remains recorded above as the evidence that led to this correction. The test race is resolved, and there are no remaining actionable findings in this spec review.

## Queue correction assessment

- The hook preserves the raw daemon error envelope, including the code.
- The service, controller, and store now consistently distinguish a reused handle with another payload (`CHAT_QUEUED_EDIT_IDEMPOTENCY_CONFLICT`) from a stale message revision (`CHAT_QUEUED_EDIT_CONFLICT`). A reused handle cannot clear an unresolved receipt.
- The four cleanup codes (`CHAT_TURN_NOT_QUEUED`, `CHAT_QUEUED_EDIT_CONFLICT`, `CHAT_QUEUED_CONTENT_INVALID`, `CHAT_QUEUED_TEXT_REQUIRED`) originate after the atomic receipt lookup, making identical recovered retries eligible for definitive cleanup as well as initial requests.
- Definitive cleanup preserves the editor text, attachment ownership, and ordinary composer. Removing the receipt enables editing/Cancel; Cancel restores the independently persisted ordinary draft.
- Cleanup uses the prepared revision CAS. A stale response cannot overwrite a replacement editor. Failed writes/readback remain fail-closed and can recover using the same receipt ID on a subsequent retry.
- The cloned queue dock now combines its parent's disabled flag with local submission state, preserving the unresolved-receipt lock.

## Independent final evidence

Seven external renderer tests passed on queue-review head `1312ff894`; the queue implementation is unchanged in the final stack:

1. Each of the four initial definitive codes unlocks the queue editor, permits Cancel, and restores the ordinary draft across remount.
2. A cleanup write failure remains locked, then recovers via the same ID.
3. A cleanup readback failure remains locked, then recovers via the same ID.
4. An unmounted old owner's rejection does not overwrite a replacement editor's persisted revision or text.

Harness and output: `queue-spec-fixed-overlay.config.mjs`, `queue-spec-fixed-overlay.log`.

```sh
cd /tmp/pr4463-split/steer/frontend
node node_modules/vitest/vitest.mjs run --config /tmp/pr4463-review-fixes-sep11/queue-spec-fixed-overlay.config.mjs src/renderer/components/chat/ChatQueuedAttachments.test.tsx -t 'SPEC fixed' --reporter verbose
```

Real HTTP/SQLite checks also passed, including two stale/availability scenarios and a separate accepted-receipt case. The accepted exact payload replays HTTP 204 after promotion and after actual Service Stop; changed payloads retain the distinct idempotency error. Definitive initial/repeated rejections create no receipt.

Harness and output: `queue-spec-fixed-go-overlay.json`, `queue-spec-fixed-overlay_test.go`, `queue-spec-fixed-go-overlay.log`.

```sh
cd /tmp/pr4463-split/steer/backend
go test -overlay=/tmp/pr4463-review-fixes-sep11/queue-spec-fixed-go-overlay.json ./internal/service/chat -run '^TestSpecQueued' -count=1 -v
```

All test additions were external transform/Go overlays; no repository source was edited. Broad suites and native validation remain the parent agent's responsibility.

---

# Initial review record — superseded by final re-review below

Read-only review of the six pinned PR diffs in `review-stack.json`. Each listed three-dot diff and corresponding `git log base..head --oneline` was executed. Diff snapshots are in `spec-diffs/5105.diff`, `5106.diff`, `5107.diff`, `5109.diff`, `5110.diff`, and `5111.diff`. No tracked source changes, pushes, or review comments were made.

## Resolved historical finding: P1 — A definitive queued-edit refusal permanently captures the composer (#5109)

Spec: accepted PR #5109 description says **“Cancel restores the independent ordinary draft”**, and the review request says **“Keep Cancel unavailable while clientMessageId is present, or provide a separate explicit-abandon action that warns the user and deliberately discards the receipt.”** The lock must apply to unresolved delivery, not every failed mutation forever.

At cumulative head `633b27ca99af5b10517bf461dd9a447959b73504`:

- `frontend/src/renderer/components/chat/ChatWorkspace.tsx:789-801` writes `saving: true` and `clientMessageId` before awaiting `onEditQueuedTurn`, but never settles the receipt on a definitive rejection.
- `ChatWorkspace.tsx:815` blocks replacing the editor; `:831-834` blocks Cancel. The composer also disables Cancel/Escape whenever `queuedEditRecovery` is true and freezes editor text. No queued-edit abandon control exists.
- `frontend/src/renderer/hooks/useConversation.ts:594` discards the API error code by constructing a plain `Error(apiErrorMessage(...))`, preventing the UI from distinguishing validation/revision refusal from transport ambiguity.

Minimal scenarios:

1. Open a queued message editor. The daemon dispatches/promotes that turn just before the save reaches it, while the renderer still has its queued snapshot. Save returns `409 CHAT_TURN_NOT_QUEUED`.
2. Alternatively, open revision 0, change the same queued message through another client (revision becomes 1), and save the stale editor. It returns `409 CHAT_QUEUED_EDIT_CONFLICT` with the instruction to reopen it.
3. In both cases the draft now retains its receipt. The editor is immutable; Cancel, Escape, replacing it, and deleting it are unavailable. Retrying always sends the same rejected payload/revision. Remount/reload preserves this trap, hiding the independent ordinary composer indefinitely.

### Reproduction evidence

External Vite transform overlay (does not modify repository source):

```sh
cd /tmp/pr4463-split/steer/frontend
node node_modules/vitest/vitest.mjs run --config /tmp/pr4463-review-fixes-sep11/queue-spec-overlay.config.mjs src/renderer/components/chat/ChatQueuedAttachments.test.tsx -t 'SPEC repro' --reporter verbose
```

Two added external tests passed, proving the faulty behavior for both server messages: receipt retained, editor `contenteditable=false`, Cancel disabled, same ID/revision on retry, and still locked after remount. These inject the same plain `Error(message)` that the real hook produces. Output: `queue-spec-overlay.log`.

External Go overlay through the real HTTP router and SQLite store:

```sh
cd /tmp/pr4463-split/steer/backend
go test -overlay=/tmp/pr4463-review-fixes-sep11/queue-spec-go-overlay.json ./internal/service/chat -run '^TestSpecQueuedDefinitiveRejectionHTTP$' -count=1 -v
```

Both `dispatched` and `stale` subtests passed. Initial save and identical replay returned their expected 409 code, and no durable receipt existed after either attempt. Overlay source: `queue-spec-overlay_test.go`. Output: `queue-spec-go-overlay.log`.

### Safe correction boundary

The service checks an existing atomic queued-edit receipt before session/controller admission and before availability/revision checks (`backend/internal/service/chat/queue.go`). The controller repeats this under its send lock. `UpdateQueuedTurnMessage` commits message mutation and receipt in one transaction. Therefore an identical accepted save returns success even if the turn has since dispatched or the controller was stopped; a same-payload `CHAT_TURN_NOT_QUEUED` is conclusive non-acceptance. `CHAT_QUEUED_EDIT_CONFLICT` currently conflates changed-payload receipt conflicts with stale revisions, so separating those codes avoids unsafe blanket cleanup. Initial definitive validation failures also need editable/cancelable recovery. An explicit abandon control can handle outcomes that remain uncertain.

## Remaining coverage and conclusions

No additional demonstrated correctness/spec findings, missing requirements, or unrequested behavior were identified in the other five PRs. User-accepted scope/size was not treated as scope creep.

- #5105: viewport callback ordering and synchronous listener detachment; production now follows development disposal ordering.
- #5106: session-incarnation isolation, accepted revision CAS, first-send refusal cleanup, shared composer mutation admission, persistence-failure boundaries, native close/quit bridge.
- #5107: staged descriptor restoration, native image payload reads, chip thumbnails from staged paths, attachment generation checks before and after awaits.
- #5109: pending-save Cancel/Escape/replacement/delete guards, stale callbacks, queue owner cleanup, atomic receipt lookup before revision/availability checks. Finding above is introduced by the strengthened exit guards composing with preexisting unconditional receipt retention.
- #5110: reservation/replay and definitive error classification, 404 missing-turn cause restoration, actual Service Stop/Start gate, durable provider-work claim, post-completion receipt repair, source close/restore/install branches, provider host descriptor/lock release and propagated shutdown errors.
- #5111: receipt-only steer recovery, no provider call on reserved/accepted replay, completion transaction, missing-controller/interface recovery, cumulative regression selection.
- Migration 0132 sets old rows to provider-work-started=1 conservatively and new reservations explicitly to 0. Adding 0131 afterward in #5111 is compatible with the repository's `goose.WithAllowMissing()` migration runner. No migration order finding.
- Completion repair requires a completed user turn with the same client handle on a branch replacing the request's source turn; it does not blindly treat queued/bound/failed turns as provider acceptance.

Scope sources read: `pr-4463.json`, `issue-5104.json`, all `pr-*-before.json`, all reviewer-thread JSONs under `/tmp/pr4463-comments-sep11`, `docs/chat-ui-improvements-checklist.md`, and relevant architecture/status guidance. Existing passing reports were not used as correctness proof. Broad suites/native validation remained the parent agent's responsibility.

## Parent validation addendum

After the independent final review, #5110 added a test-only correction at `40bdc0d47e10eaea567ebdc0d06587e8001dd3ea`: an older workspace regression now checks that Escape preserves the unresolved queue receipt when the target disappears. Its earlier assertion expected the receipt to be discarded, contradicting the corrected cancellation contract. The full 112-test ChatWorkspace file passed after correction. #5111 was restacked to `b7ae607de5d3102ed9281575292d3352a9b87b64`; no implementation code changed after the independently reviewed shutdown guard. The current validation report records the complete suites and final published heads.

Final tooling-only update: #5110 changed the shutdown fixture’s `h.fakeConversation.Close()` selector to its equivalent promoted `h.Close()` call, as required by pinned staticcheck QF1008. The complete pinned linter then passed with zero issues. No implementation behavior changed; the final heads are recorded in the manifest.
