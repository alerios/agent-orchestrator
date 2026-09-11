# Metrics Phase 1: Metrics Tab and Ungate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Metrics tab to the session inspector rail and move the existing usage block into it, removing the `developerMode` gate so the token and cost data AO already computes becomes visible.

**Architecture:** Purely presentational. No backend change, no new ingestion, no new data. `InspectorView` in the shared `@aoagents/product-ui` package gains a `"metrics"` member and the shell gains a `metricsView` slot; the frontend moves `UsageCostTelemetry` out of `SummaryView` into a new `MetricsView`, dropping the `developerMode` condition. Developer mode continues to gate only diagnostics.

**Tech Stack:** TypeScript, React 19, Vitest, Testing Library, i18next (flat dot-notation keys, 8 locales), Tailwind.

**Spec:** `docs/superpowers/specs/2026-09-11-session-metrics-and-efficiency-scoring-design.md`

## Global Constraints

- No hardcoded English string may appear in `frontend/src/renderer`. `frontend/src/renderer/i18n/renderer-coverage.test.ts` fails the build on any display literal not routed through an i18n key or listed in its `approvedLiterals` allowlist.
- Every new i18n key must be added to **all eight** locale files in `frontend/src/renderer/i18n/`: `en.json`, `de.json`, `es.json`, `fr.json`, `ja.json`, `ko.json`, `pt-BR.json`, `zh-CN.json`. Keys are flat and dot-separated (`"inspector.usage.cost"`), not nested objects. Non-English locales take the English string until translated.
- **Read `DESIGN.md` before any visual decision**, starting with its "clone agent-orchestrator verbatim" banner — that banner governs the current look and supersedes the older design-reference framing. Build the new tab from the existing `@aoagents/product-ui` primitives and `components/ui/*` shadcn components; the tab icon and section layout must match the four tabs already there. Do not introduce new visual patterns without explicit approval.
- When demoing this change, run `ao preview [url]` from inside the session so it renders in the inspector rail's Browser tab, and say "check the Browser tab" in your reply — the panel badges as unseen rather than stealing focus.
- `packages/product-ui` is a shared package consumed by the frontend. Changes there must keep `npm run product-ui:check` green (typecheck + tests + build).
- Absent data renders as an explicit unavailable state, never as `0`. This is the spec's refusal-to-fabricate rule and applies to every block added in this plan.
- Do not change the response shape of `GET /api/v1/usage/sessions/{sessionId}`. This phase consumes it exactly as it exists.

---

## File Structure

| File | Responsibility |
|---|---|
| `packages/product-ui/src/SessionInspectorView.tsx` | Modify: add `"metrics"` to `InspectorView`, add `metricsView` slot and its body-class handling |
| `packages/product-ui/src/SessionInspectorView.test.tsx` | Modify: cover the new tab's rendering and keyboard navigation |
| `frontend/src/renderer/components/SessionInspectorMetrics.tsx` | Create: `MetricsView` plus the cost/token block moved out of `SessionInspector.tsx` |
| `frontend/src/renderer/components/SessionInspector.tsx` | Modify: add the tab def, wire `metricsView`, remove the usage block and its `developerMode` gate from `SummaryView` |
| `frontend/src/renderer/components/SessionInspectorMetrics.test.tsx` | Create: unavailable / partial / complete rendering, and ungated rendering |
| `frontend/src/renderer/i18n/*.json` | Modify: new `inspector.metrics.*` keys in all 8 locales |

A new file rather than growing `SessionInspector.tsx`: that file is already 2,522 lines, and the Metrics tab will keep accruing blocks in phases 3-5. Putting it in its own module now avoids a painful split later.

---

### Task 1: Add the metrics view to the shared inspector shell

**Files:**
- Modify: `packages/product-ui/src/SessionInspectorView.tsx:25` (the `InspectorView` union), `:39-65` (shell props), `:161-178` (body classes and panel switch)
- Test: `packages/product-ui/src/SessionInspectorView.test.tsx`

**Interfaces:**
- Consumes: nothing from earlier tasks (first task).
- Produces: `InspectorView` union now includes `"metrics"`; `SessionInspectorShellView` accepts a new optional prop `metricsView?: ReactNode` and renders it when `activeView === "metrics"`. Task 3 consumes both.

- [ ] **Step 1: Write the failing test**

Add to `packages/product-ui/src/SessionInspectorView.test.tsx`, inside the existing `describe` for the shell:

```tsx
it("renders the metrics panel when the metrics tab is active", () => {
  render(
    <SessionInspectorShellView
      activeView="metrics"
      ariaLabel="Inspector"
      browserPoppedOut={false}
      metricsView={<p>metrics body</p>}
      onViewChange={() => {}}
      summaryView={<p>summary body</p>}
      tabs={[
        { icon: <span />, id: "summary", label: "Summary" },
        { icon: <span />, id: "metrics", label: "Metrics" },
      ]}
    />,
  );
  expect(screen.getByText("metrics body")).toBeInTheDocument();
  expect(screen.queryByText("summary body")).not.toBeInTheDocument();
});

it("moves selection to the metrics tab with ArrowRight", () => {
  const onViewChange = vi.fn();
  render(
    <SessionInspectorShellView
      activeView="summary"
      ariaLabel="Inspector"
      browserPoppedOut={false}
      metricsView={<p>metrics body</p>}
      onViewChange={onViewChange}
      summaryView={<p>summary body</p>}
      tabs={[
        { icon: <span />, id: "summary", label: "Summary" },
        { icon: <span />, id: "metrics", label: "Metrics" },
      ]}
    />,
  );
  fireEvent.keyDown(screen.getByRole("tab", { name: "Summary" }), { key: "ArrowRight" });
  expect(onViewChange).toHaveBeenCalledWith("metrics");
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm --prefix packages/product-ui test -- SessionInspectorView`

Expected: FAIL. The first test fails because `metricsView` is not a known prop and nothing renders for `activeView="metrics"`; TypeScript also rejects `"metrics"` as an `InspectorView`.

- [ ] **Step 3: Widen the union and add the slot**

In `packages/product-ui/src/SessionInspectorView.tsx`, change line 25:

```ts
export type InspectorView = "summary" | "metrics" | "reviews" | "browser" | "files";
```

Add `metricsView?: ReactNode;` to both the destructured parameter list and the prop type of `SessionInspectorShellView`, placed alphabetically next to `loadingText`:

```tsx
	isVisible = true,
	loadingText,
	metricsView,
	onViewChange,
```

```ts
	isVisible?: boolean;
	loadingText?: string;
	metricsView?: ReactNode;
	onViewChange: (view: InspectorView) => void;
```

Render it in the panel switch (after the `summary` line):

```tsx
				{activeView === "summary" ? summaryView : null}
				{activeView === "metrics" ? metricsView : null}
```

The metrics panel wants the same scrollable padded body as summary, which it already gets: the body-class condition is `activeView !== "browser" && activeView !== "files"`, so `"metrics"` receives `inspectorScrollableBodyClass` with no change needed.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm --prefix packages/product-ui test -- SessionInspectorView`

Expected: PASS, both new tests and all pre-existing ones.

- [ ] **Step 5: Verify the package still typechecks and builds**

Run: `npm run product-ui:check`

Expected: typecheck, tests and build all pass. This catches any other consumer that switches exhaustively on `InspectorView`.

- [ ] **Step 6: Commit**

```bash
git add packages/product-ui/src/SessionInspectorView.tsx packages/product-ui/src/SessionInspectorView.test.tsx
git commit -m "feat(product-ui): add metrics view to session inspector shell"
```

---

### Task 2: Add the i18n keys

**Files:**
- Modify: `frontend/src/renderer/i18n/en.json`, `de.json`, `es.json`, `fr.json`, `ja.json`, `ko.json`, `pt-BR.json`, `zh-CN.json`

**Interfaces:**
- Consumes: nothing.
- Produces: the keys `inspector.metrics`, `inspector.metrics.noData`, `inspector.metrics.coverage.title`, `inspector.metrics.coverage.full`, `inspector.metrics.coverage.partial`, `inspector.metrics.coverage.none`. Task 3 uses all six via `t()`.

Note: `inspector.usage.*` keys already exist (including `inspector.usage.cost`, `inspector.usage.processedTokens`, `inspector.usage.cacheHitRate`) and are reused unchanged — do not duplicate them.

- [ ] **Step 1: Add the keys to every locale**

Run this script from the repository root. It inserts the same six keys into all eight files and keeps them sorted, matching the flat dot-notation convention:

```bash
python3 - <<'PY'
import json, pathlib

keys = {
    "inspector.metrics": "Metrics",
    "inspector.metrics.noData": "No telemetry recorded for this session yet.",
    "inspector.metrics.coverage.title": "Coverage",
    "inspector.metrics.coverage.full": "AO observed this session's full token and cost usage.",
    "inspector.metrics.coverage.partial": "Some usage for this session could not be measured, so totals are a lower bound.",
    "inspector.metrics.coverage.none": "AO cannot measure usage for the {{harness}} agent.",
}

for path in sorted(pathlib.Path("frontend/src/renderer/i18n").glob("*.json")):
    data = json.loads(path.read_text(encoding="utf-8"))
    for key, value in keys.items():
        data.setdefault(key, value)
    ordered = {k: data[k] for k in sorted(data)}
    path.write_text(json.dumps(ordered, ensure_ascii=False, indent="\t") + "\n", encoding="utf-8")
    print("updated", path)
PY
```

- [ ] **Step 2: Verify the keys landed in all eight files**

Run: `grep -l '"inspector.metrics.coverage.none"' frontend/src/renderer/i18n/*.json | wc -l`

Expected: `8`

- [ ] **Step 3: Verify the i18n tests still pass**

Run: `npm --prefix frontend test -- i18n`

Expected: PASS. `instance.test.ts` checks locale parity; if it reports a key ordering or formatting mismatch, re-run the script — it rewrites with tab indentation to match the existing files.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/renderer/i18n
git commit -m "i18n: add inspector metrics tab keys"
```

---

### Task 3: Create the ungated MetricsView

**Files:**
- Create: `frontend/src/renderer/components/SessionInspectorMetrics.tsx`
- Create: `frontend/src/renderer/components/SessionInspectorMetrics.test.tsx`
- Reference (do not yet edit): `frontend/src/renderer/components/SessionInspector.tsx:400-530` holds `UsageCostTelemetry`, `UsageAgentAttribution`, `UsageProviderRow`, `UsageMetrics`, `ProviderUsageDetails`

**Interfaces:**
- Consumes: `InspectorView` including `"metrics"` and the `metricsView` slot from Task 1; the six i18n keys from Task 2.
- Produces: `export function MetricsView({ session }: { session: WorkspaceSession }): ReactNode`. Task 4 passes it into the shell.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/renderer/components/SessionInspectorMetrics.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MetricsView } from "./SessionInspectorMetrics";

const session = { harness: "claude-code", id: "sess-1" } as never;

vi.mock("../hooks/useSessionUsage", () => ({
  useSessionUsage: () => mockUsage,
}));

let mockUsage: unknown;

describe("MetricsView", () => {
  it("renders cost and tokens without developer mode", () => {
    mockUsage = {
      data: {
        harnesses: [],
        incomplete: false,
        sessionId: "sess-1",
        totals: {
          estimatedCost: { currency: "USD", nanos: 2840000000 },
          processedTokens: 207000,
        },
      },
      isError: false,
      isLoading: false,
    };
    render(<MetricsView session={session} />);
    expect(screen.getByTestId("session-usage-metrics")).toBeInTheDocument();
  });

  it("states that no telemetry exists rather than rendering zeros", () => {
    mockUsage = { data: undefined, isError: false, isLoading: false };
    render(<MetricsView session={session} />);
    expect(screen.getByText(/No telemetry recorded/i)).toBeInTheDocument();
    expect(screen.queryByTestId("session-usage-metrics")).not.toBeInTheDocument();
  });

  it("flags partial coverage as a lower bound", () => {
    mockUsage = {
      data: {
        harnesses: [],
        incomplete: true,
        sessionId: "sess-1",
        totals: { estimatedCost: null, processedTokens: 1000 },
      },
      isError: false,
      isLoading: false,
    };
    render(<MetricsView session={session} />);
    expect(screen.getByText(/lower bound/i)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm --prefix frontend test -- SessionInspectorMetrics`

Expected: FAIL with a module resolution error — `SessionInspectorMetrics.tsx` does not exist.

- [ ] **Step 3: Create the component**

Create `frontend/src/renderer/components/SessionInspectorMetrics.tsx`. Move `UsageCostTelemetry`, `UsageAgentAttribution`, `UsageProviderRow`, `UsageMetrics` and `ProviderUsageDetails` here verbatim from `SessionInspector.tsx` (they are self-contained; keep their `export` status unchanged except where noted), then add:

```tsx
export function MetricsView({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const developerMode = useUiStore((state) => state.developerMode);
	// Always fetch: the usage block is no longer developer-gated.
	const usageQuery = useSessionUsage(session.id, true);
	const usage = usageQuery.data;
	const hasUsage = !usageQuery.isLoading && !usageQuery.isError && hasMeaningfulSessionUsage(usage);
	return (
		<div role="tabpanel">
			{hasUsage && usage ? (
				<>
					<Section title={t("inspector.usage.title")}>
						<UsageCostTelemetry usage={usage} />
					</Section>
					<Section title={t("inspector.metrics.coverage.title")}>
						<p className={inspectorEmptyClass}>
							{usage.incomplete
								? t("inspector.metrics.coverage.partial")
								: t("inspector.metrics.coverage.full")}
						</p>
					</Section>
				</>
			) : (
				<Section title={t("inspector.metrics")}>
					<p className={inspectorEmptyClass}>
						{usageQuery.isError
							? t("inspector.usage.processedTokensUnavailable")
							: t("inspector.metrics.noData")}
					</p>
				</Section>
			)}
			{developerMode ? <MetricsDiagnostics query={usageQuery} /> : null}
		</div>
	);
}

/** Developer-mode-only ingestion diagnostics. Not user-facing telemetry. */
function MetricsDiagnostics({ query }: { query: ReturnType<typeof useSessionUsage> }) {
	const { t } = useTranslation();
	if (!query.isError) return null;
	return (
		<Section title={t("inspector.metrics.coverage.title")}>
			<p className={inspectorEmptyClass}>{t("inspector.usage.processedTokensUnavailable")}</p>
		</Section>
	);
}
```

Import `hasMeaningfulSessionUsage` from wherever `SessionInspector.tsx` currently imports it, and `InspectorSection as Section` plus `inspectorEmptyClass` from `@aoagents/product-ui`.

The `developerMode` read is deliberately retained but now governs *only* `MetricsDiagnostics` — the spec's stated purpose for that flag.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm --prefix frontend test -- SessionInspectorMetrics`

Expected: PASS, all three tests.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/renderer/components/SessionInspectorMetrics.tsx frontend/src/renderer/components/SessionInspectorMetrics.test.tsx
git commit -m "feat(inspector): add ungated metrics view with coverage statement"
```

---

### Task 4: Wire the tab in and remove the old gated block

**Files:**
- Modify: `frontend/src/renderer/components/SessionInspector.tsx` — `VIEW_DEFS` (`:91-131`), the `labelKey` union (`:94`), the shell call (`:220-245`), `SummaryView` (`:264-330`), and delete the usage components moved in Task 3
- Test: `frontend/src/renderer/components/SessionInspector.test.tsx`

**Interfaces:**
- Consumes: `MetricsView` from Task 3; `metricsView` prop and `"metrics"` union member from Task 1; i18n keys from Task 2.
- Produces: a five-tab inspector. No exported API change.

- [ ] **Step 1: Write the failing test**

Add to `frontend/src/renderer/components/SessionInspector.test.tsx`:

```tsx
it("offers a metrics tab and no longer shows usage on the summary tab", async () => {
  renderInspector({ session: sessionWithUsage });
  expect(screen.getByRole("tab", { name: "Metrics" })).toBeInTheDocument();
  expect(screen.queryByTestId("session-usage-metrics")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("tab", { name: "Metrics" }));
  expect(await screen.findByTestId("session-usage-metrics")).toBeInTheDocument();
});
```

Reuse the file's existing render helper and session fixture; name them as that file already does rather than introducing `renderInspector`/`sessionWithUsage` if equivalents exist.

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm --prefix frontend test -- SessionInspector.test`

Expected: FAIL — no tab named "Metrics" exists.

- [ ] **Step 3: Add the tab definition**

In `SessionInspector.tsx`, widen the `labelKey` union on line 94:

```ts
	labelKey: "inspector.summary" | "inspector.metrics" | "inspector.reviewTab" | "inspector.browser" | "inspector.files";
```

Insert a `VIEW_DEFS` entry immediately after the `summary` entry, so Metrics sits second:

```tsx
	{
		id: "metrics",
		labelKey: "inspector.metrics",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<line x1="4" y1="20" x2="4" y2="12" />
				<line x1="10" y1="20" x2="10" y2="4" />
				<line x1="16" y1="20" x2="16" y2="9" />
				<line x1="22" y1="20" x2="22" y2="15" />
			</svg>
		),
	},
```

- [ ] **Step 4: Pass the panel into the shell**

Add to the `SessionInspectorShellView` call, alphabetically before `onViewChange`:

```tsx
				metricsView={session ? <MetricsView session={session} /> : undefined}
```

Import it: `import { MetricsView } from "./SessionInspectorMetrics";`

- [ ] **Step 5: Remove the gated usage block from SummaryView**

In `SummaryView`, delete the `usageQuery`, `showUsage` and `showUsageError` declarations (`:278-284`) and the entire `usage={...}` prop passed to `SessionInspectorSummaryView` (`:316-327`). Delete the now-unused `useSessionUsage` / `SessionUsage` import and the five usage components moved in Task 3.

If `SessionInspectorSummaryView` requires `usage`, pass `usage={undefined}`; check its prop type in `packages/product-ui/src/SessionInspectorView.tsx` first and only make it optional there if it is currently required.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `npm --prefix frontend test -- SessionInspector`

Expected: PASS. Pre-existing tests asserting usage on the summary tab will now fail correctly — update them to click the Metrics tab first, which is the intended behaviour change.

- [ ] **Step 7: Verify typecheck and the no-hardcoded-copy rule**

Run: `npm run frontend:typecheck && npm --prefix frontend test -- renderer-coverage`

Expected: both PASS. A failure in `renderer-coverage` means a literal string slipped into the new component — route it through an i18n key.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/renderer/components/SessionInspector.tsx frontend/src/renderer/components/SessionInspector.test.tsx
git commit -m "feat(inspector): move usage telemetry to the metrics tab, ungated"
```

---

### Task 5: Verify in the running app

**Files:** none — this is a manual verification gate before the phase is called done.

- [ ] **Step 1: Start AO**

Run the project's normal dev command (see `docs/development.md`).

- [ ] **Step 2: Confirm the ungated metrics**

Open a worker session that has recorded usage — the board cards showing a dollar figure identify one. With **developer mode off**, open the Metrics tab.

Expected: cost and the token vector render; the Coverage block states whether usage was fully observed.

- [ ] **Step 3: Confirm the honest empty state**

Open a session on a harness with no certified usage source (anything other than Claude Code, Codex or Kimi).

Expected: the Metrics tab states that no telemetry was recorded. It must **not** show `$0.00` or `0 tokens`.

- [ ] **Step 4: Confirm Summary is unchanged apart from the removal**

Expected: PR cards, reviews and controls all still render on Summary; the usage block is gone from it.

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "fix(inspector): address metrics tab verification findings"
```

---

## Phase Completion

Run before declaring the phase done:

```bash
npm run product-ui:check
npm run frontend:typecheck
npm --prefix frontend test
```

All three must pass. Phase 2 (`opencode` usage source) is independent of this work and may proceed in parallel.
