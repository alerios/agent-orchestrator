# Conflict resolution — 10 September 2026

The six open implementation PRs were rebased onto main `8343bcc089982e202f0fbc37ff43aa09de3110d4` and restacked in the same dependency order. Original #4463 remains the superseded draft.

| PR | Previous head | Rebased head | Actual base |
| --- | --- | --- | --- |
| #5105 | `40816926f2bf` | `fabbcd6ada7f` | `main` |
| #5106 | `db63563e4b8a` | `ff2440b5df1e` | `codex/fix-xterm-disposal` |
| #5107 | `7ff1ca3a5f15` | `92bf1117f670` | `codex/chat-draft-text` |
| #5109 | `a03d533b1a1d` | `7edf3fbb2931` | `codex/chat-draft-attachments` |
| #5110 | `3829c4dcbfe4` | `8bd0986ffdc3` | `codex/chat-draft-queue` |
| #5111 | `bfa0f6a58c6e` | `8999dbddcc43` | `codex/chat-draft-inline` |

## Resolutions

- #5105: main's #5009 added a development-only disposal deferral alongside cursor-position reporting and replay handling. Preserve the new terminal protocol behavior and retain this PR's unconditional zero-delay disposal. Extend the test to development and production modes, preserving the disposal spy and asserting initialization-before-disposal ordering.
- #5109: preserve main's topbar-only switch/reconnect status while retaining the queued-delivery recovery exception in the composer disabled condition.
- #5110: preserve the new absence of duplicate composer status text and retain the inline-edit inert-state assertion.
- #5106, #5107, #5111: replayed cleanly. A range comparison confirms that the prior corrections for first-send refusals, stale asynchronous dispatch, staged thumbnails, queued ownership, and receipt-only recovery remain intact.

The reviewed feature scope is unchanged. All updated descriptions use net counts against their configured bases.

## Terminal regression evidence

The same strengthened test was run against current main's terminal implementation and the resolved #5105 implementation:

| Implementation | Development (`DEV=true`) | Production (`DEV=false`) |
| --- | --- | --- |
| Main `8343bcc08998` | Pass | Fail: terminal disposed before queued initialization |
| #5105 `fabbcd6ada7f` | Pass | Pass |

The complete terminal file passes all **99 tests** on the updated head. This is a deterministic lifecycle-ordering regression using the test xterm implementation. It is not a new native Electron recording or a claim that every production navigation reproduces the race.

- [Main regression output](xterm-main-regression.log)
- [Updated terminal suite output](xterm-focused.log)
- [Commit range comparison](range-diff.txt)

Earlier screenshots and screen recordings are retained in the PR descriptions with their recorded pre-rebase commits. Main has since acquired the development-only fix, so those historical StrictMode recordings do not demonstrate the remaining difference against today's main.

See [validation](validation.md) for fresh local and CI results.
