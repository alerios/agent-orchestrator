# Validation — review corrections, 11 September 2026

The agreed six-PR scope and review order are preserved. Each PR was reviewed against its actual stacked base, pinned in [the review manifest](review-stack.json). Original #4463 remains draft; no implementation PR was merged.

## Frontend

All three changed PRs ran complete frontend and E2E typechecks, complete Vitest suites and all 59 renderer smoke tests. Local environment: macOS arm64, Node 24.14.0, pinned agent-browser runtime. External configs isolate test caches and server ports for these task worktrees. The first three PRs are unchanged at their previously fully validated heads; their current-head CI was checked again.

| PR | Final head | Vitest passed / skipped | Smoke passed | Validation |
| --- | --- | --- | --- | --- |
| #5105 | `fabbcd6ada7f` | 4,120 / 6 | 59 | Same head, 10 September pass |
| #5106 | `ff2440b5df1e` | 4,201 / 6 | 59 | Same head, 10 September pass |
| #5107 | `92bf1117f670` | 4,206 / 6 | 59 | Same head, 10 September pass |
| #5109 | `16a908352807` | 4,229 / 6 | 59 | 11 September rerun |
| #5110 | `e992529fcde2` | 4,265 / 6 | 59 | 11 September rerun |
| #5111 | `c9eb726ad6d2` | 4,274 / 6 | 59 | 11 September rerun |

## Backend and generated code

- Complete `go build ./...`, `go vet ./...`, and `go test -race -timeout=15m -p 2 ./...` passed on the corrected cumulative stack. Build and vet were repeated after the final source changes.
- After the last shutdown-guard/test changes, the entire chat service race suite was repeated successfully. The later frontend-only test correction does not change that backend tree.
- golangci-lint v2.12.2 passed with a fresh task cache; Go formatting is clean.
- Complete native macOS CLI E2E suite (`go test -tags=e2e -v ./internal/cli/...`) passed.
- The opt-in chat recovery gate passed, including cancellation after reservation, repair after a lost completion write and at-most-once delivery.
- `npm run api`, `npm run sqlc`, and generated-file drift checks passed separately on #5109, #5110 and #5111. Migration 0132 is registered in the immutable migration-version ledger; 0131 remains the steer slice's migration. The existing runner supports applying these stacked migrations out of numeric order.
- CI selects Go from go.mod. Its normal automatic toolchain selection, reproduced locally with `GOTOOLCHAIN=go1.25.7+auto`, selects Go 1.26.5 because the checked-in go.work requires it. An initial strict 1.25.7 attempt was rejected before compilation and is not a validation pass.

Shared cloud-client generation/typechecks/tests/pack dry run, product-ui typechecks/tests/pack dry run, landing icon checks, and native macOS helper tests/builds were already fully passing. Their relevant source objects were verified unchanged, recorded in [shared validation](unchanged-shared-validation.json). The current-head Frontend jobs also rerun the shared checks.

## Failed attempts and local limits

- Running Electron rebuilt shared better-sqlite3 for Electron's ABI. An early Node Vitest attempt consequently failed 12 unchanged browser-profile-import tests. Electron was closed, better-sqlite3 was rebuilt for Node 24, and the complete suites were rerun successfully.
- The first queue full suite hit an unchanged CreateProjectFlow disabled-button assertion during asynchronous repository-name validation. All 43 tests in that file passed in isolation, the complete suite passed on rerun with two workers, and current-head CI passed. No project-creation implementation or test was modified.
- The first inline full suite found an older test that expected Escape to erase an unresolved queued-edit receipt. That contradicted the corrected cancellation contract; its assertion now requires the receipt to survive. The complete 112-test workspace file and full suite passed afterward.
- Independent review caught a timing assumption in the added shutdown regression: the stopped source can already be removed by its watcher. The assertion now permits removal while forbidding replacement, credential rotation and provider reattachment. Ten consecutive race-detector runs passed. The test uses the suite timeout rather than placing expensive fixture migration inside a five-second request context.
- Pinned staticcheck required a shorter embedded-field selector in the shutdown test fixture. That equivalent selector was corrected. A concurrent local linter invocation was rejected by the runner lock; the final serial run passed with zero issues.
- The local Playwright browser was initially missing. Chromium was installed and all 59 smoke cases were rerun successfully for each changed branch.
- Docker did not answer `docker info` within 15 seconds. The Linux fresh-install container and native Windows/Linux jobs are verified by CI, not presented as local passes. No publishing or production deployment was used as validation.

## Current-head CI

All 18 latest workflows / 48 jobs passed on the final published heads. Obsolete runs from superseded inline/steer heads were cancelled; only the final-head results appear below.

| PR | Passing workflows |
| --- | --- |
| #5105 | [gitleaks](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34406776763) · [Frontend](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34406776764) |
| #5106 | [gitleaks](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34406777337) · [Frontend](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34406777350) |
| #5107 | [Frontend](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34406779046) · [gitleaks](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34406779097) |
| #5109 | [gitleaks](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34571020735) · [Go](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34571020710) · [CLI E2E](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34571020678) · [Frontend](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34571020653) |
| #5110 | [Frontend](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34572824370) · [gitleaks](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34572824548) · [CLI E2E](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34572824509) · [Go](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34572824537) |
| #5111 | [Frontend](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34572821832) · [CLI E2E](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34572821898) · [gitleaks](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34572821849) · [Go](https://github.com/Untrivial-ai/agent-orchestrator/actions/runs/34572821890) |

[Machine-readable current-head CI](ci-results.json) · [Local results, including first attempts and final reruns](local-results.json).

## Evidence limits

[The real Electron screenshots and replay result](README.md#real-electron-and-provider-evidence) name their recorded commit. They demonstrate a genuine provider response, controller stop/resume and same-ID replay with one stored replacement message. They do not pretend that a screenshot alone proves a crash at a particular database instruction. Those boundaries have deterministic real-service/SQLite and detached-host regression evidence, linked in the report.

The independent reviews found no remaining actionable correctness or documented-standard findings in the corrected implementation. Two optional duplication/prop-group simplifications remain nonblocking judgments in the standards report. This is evidence for reviewer reassessment, not a guarantee that no undiscovered bug exists. Existing human change requests still require the reviewers' reassessment.
