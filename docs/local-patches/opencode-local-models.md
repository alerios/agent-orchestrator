# OpenCode + local models in Agent Orchestrator

How to point an AO project's `opencode` sessions at locally-hosted Ollama
models instead of a cloud provider, what actually happens when you do, and
the one configuration verified to work end-to-end on consumer hardware with
no discrete GPU.

This is AO's side of a joint investigation with the
[`ai-local-models`](../../../ai-magic/ai-local-models/README.md) local-inference
setup (a sibling repo; see `~/Tech/ai-magic/ai-local-models/README.md` on the
reference machine). That repo owns the Ollama services, model choices, and
`opencode.json` provider config; this doc owns how AO's project config,
session roles, and system
prompts interact with that setup once it's wired in.

## Reference hardware

HP EliteBook 840 G11 laptop, Intel Core Ultra 5 125U (Meteor Lake), no
discrete GPU, integrated graphics only, 30GB RAM, Thunderbolt 4 (no internal
GPU slot). Two Ollama services from `ai-local-models`'s systemd units:

| Provider ID | Service | Port | Backend |
|---|---|---|---|
| `ai-local-models-gpu` | `ollama-gpu.service` | `11434` | Vulkan (Intel iGPU) |
| `ai-local-models-cpu` | `ollama-cpu.service` | `11435` | CPU |

Numbers below are specific to this machine. The *mechanisms* (Vulkan
device-loss at a fixed context size, ollama's prompt-cache-assisted retries,
opencode's tool-schema token floor) generalize to any Intel-iGPU-only
machine; the exact thresholds won't.

## Wiring a project to a local model

Point a project's orchestrator or worker role at opencode with a local
model via the project config's `agentConfig.model`, addressed as
`<opencode-provider-id>/<model-tag>`:

```json
{
  "worker": {
    "agent": "opencode",
    "agentConfig": { "model": "ai-local-models-cpu/gemma4:e2b" }
  }
}
```

This is the same shape as any other harness/model override — nothing
AO-specific is needed to *select* a local model. What AO does need to be
configured well is `~/.opencode/opencode.json` on the machine running the
worker (see `ai-local-models`'s README for the full file), and that's where
most of the findings below live.

## Finding 1: the Vulkan iGPU backend crashes at a fixed context size

`ollama-gpu` looked like the fast path — 35–95 tok/s prompt eval vs. CPU's
9–23 tok/s. It crashes instead, reliably, once a turn's cumulative context
(prompt + generated tokens) crosses roughly **3,072 tokens**:

```text
srv update_slots: decode() failed: vk::Queue::submit: ErrorDeviceLost
terminate called after throwing an instance of 'vk::DeviceLostError'
llama-server terminated | error="signal: aborted (core dumped)"
```

This was reproduced identically at 7,289 total tokens and at 11,057 total
tokens — the crash point itself didn't move with the total prompt size,
which is why it reads as a batch/allocation boundary bug in the Vulkan
backend rather than a memory-exhaustion ceiling that would scale with
context. It happened across every model tried (`qwen3.5:4b`, `qwen2.5-coder:7b`,
`gpt-oss:20b`) and in both AO's `orchestrator` and `worker` roles.

**Any real AO/opencode agent turn is well over 3,072 tokens** — even a
worker's smallest observed turn (opencode's own tool-calling overhead atop
AO's injected system prompt) started around 6,700–7,300 tokens after the
tool-trim in Finding 3. So on this class of hardware, `ollama-gpu` is not
viable for actual AO sessions, only for genuinely tiny bare completions.
**Route AO's opencode sessions through the CPU provider.** If you have a
real (non-iGPU) GPU, this constraint may not apply — verify with
`opencode debug agent <name>` plus a real session before trusting GPU
routing on any Intel-iGPU-only machine.

## Finding 2: CPU is stable, but the first cold call is the one that struggles

CPU throughput on this hardware (9–23 tok/s prompt eval, 1.5–5.8 tok/s
decode, both varying a lot with concurrent system load) means a **cold**
request carrying a full agentic tool-calling context can take longer than a
client's fixed provider-response timeout. OpenCode's own default is 300,000ms
(5 minutes); AO inherits whatever the ACP client sets. A cold ~11K-token
orchestrator turn or ~7K-token worker turn can genuinely need 10–15+ minutes
of wall time on this hardware under load — well past that window.

What makes this recoverable rather than a hard failure: Ollama keeps a
server-side KV-cache checkpoint of whatever prefix it already processed,
even from a request that timed out client-side:

```text
task 860 | new prompt, task.n_tokens = 6762
task 860 | cached n_tokens = 5737, memory_seq_rm [5737, end)
prompt processing, n_tokens = 1021, ... 64.08s
```

Each retry (opencode retries a timed-out provider call automatically) only
has to process the tokens past the last cached point, so the *n*th retry is
faster than the first, and the gap narrows every time. In this investigation,
every CPU-backed test that was left running through its natural retries
eventually completed correctly; every failure was a test cut off early by an
artificial timeout in the test harness, not a genuine dead end.

**Practically:** don't judge a CPU-routed local-model AO session as
non-functional from one timed-out first turn. The first turn against a new
tool/prompt configuration is the expensive one; subsequent turns — even in a
different session, as long as the prompt prefix matches — are much cheaper.
If you need a fast first response, warm the cache deliberately (a throwaway
call with the same system prompt) before the turn that matters.

## Finding 3: trim OpenCode's own tool surface, not just AO's prompt

AO's injected system prompt is *not* the biggest cost. Using opencode's own
introspection:

```bash
opencode debug agent build   # dumps the fully-resolved tool/permission list
opencode debug skill         # lists every auto-discovered skill
```

revealed the default agent auto-discovers every skill under `~/.claude/skills/*`
and `~/.config/opencode/skills/*` (19 skills on the reference machine) and
lists each one's name + description inside the `skill` tool's own schema —
sent on **every** turn whether or not the tool is ever called — plus a
`permission` entry per skill directory. Combined with the other built-in
tools (`bash`, `read`, `glob`, `grep`, `edit`, `write`, `task`, `webfetch`,
`todowrite`, `question`), this floor dominates over AO's own
`orchestratorRules`/task-prompt content, and it's paid identically whether
the session is an AO `orchestrator` or a `worker` — an early hypothesis that
"worker" sessions would carry a much smaller floor did not hold up: AO's own
injected prompt is smaller for a worker (measured 8,974 bytes vs. an
orchestrator's larger one), but opencode's own tool-schema/skill-menu
overhead is closer to a shared constant, and it's what actually dominates
total context size.

Disabling the tools not needed for a narrow local-model task:

```json
{
  "tools": {
    "skill": false,
    "webfetch": false,
    "todowrite": false,
    "task": false,
    "question": false
  }
}
```

measurably cut a real turn from 11,057 to 7,289 tokens (~34%) — verified by
diffing `opencode debug agent build` before/after and by watching
`task.n_tokens` in `ollama`'s own logs for a real spawned AO session. This is
worth doing for any local-model routing regardless of GPU/CPU backend: it
reduces prefill cost and (via `skill: false`) removes tokens that were never
going to be useful to a session with no interactive human to hand a skill's
instructions to anyway.

This can be applied globally (`~/.opencode/opencode.json`, affecting every
opencode session including cloud-model ones) or scoped specifically to
AO-spawned local-model sessions by extending
`PrepareACPConfigContent`/`opencodeAgentSettings` in
[`backend/internal/adapters/agent/opencode/opencode.go`](../backend/internal/adapters/agent/opencode/opencode.go)
— that's where AO already builds the per-session `agent["ao-<sessionID>"]`
config entry it injects via `OPENCODE_CONFIG_CONTENT`; a `tools` field there,
conditioned on the session's model being one of the local providers, would
trim only local-model AO sessions and leave cloud-model sessions (Claude
Code, Codex, etc.) untouched. This has not been implemented yet — the global
config-file change was tested first and is what's verified below.

**One catch:** opencode does not hot-reload config
(`opencode debug` and its own `customize-opencode` skill both say so
explicitly). A config change only takes effect for sessions spawned *after*
the change — an already-running AO session keeps whatever tool surface it
started with.

## Finding 4: `reasoning_effort` is not just a `qwen3.5` thing

Don't assume a model has no hidden "thinking" mode just because it isn't
marketed as a reasoning model. `gemma4:e2b` — configured in `ai-local-models`
purely for CPU vision triage — turned out to default to emitting a hidden
reasoning block same as `qwen3.5:4b`:

```text
"reply with exactly PONG", no reasoning_effort  -> 91 completion tokens, leaked "Thinking Process:" block
same prompt, reasoning_effort: "none"           -> 3 completion tokens, clean answer
```

This was caught only by inspecting a raw response for a `reasoning` field
before trusting a model's config, not by assuming based on the model's
marketing/vision role. Check every local model you route through AO, not
just the ones you expect to reason. See `ai-local-models`'s README for the
full mechanism (why `"think": false` / `/no_think` / `chat_template_kwargs`
don't work over Ollama's OpenAI-compatible endpoint and `reasoning_effort`
does) — this doc only calls out that the checklist applies more broadly than
it first appears to.

## The verified end-to-end configuration

The one AO configuration confirmed working, twice, unattended, through a
real orchestrator → worker delegation with actual file writes and command
execution:

- **Orchestrator**: `claude-code` (cloud/local Claude, unaffected by any of
  the above — this is the coordinating role, kept on a model that isn't
  hardware-constrained).
- **Worker**: `opencode` / `ai-local-models-cpu/gemma4:e2b`, with the
  tool-trim (Finding 3) and `reasoning_effort: "none"` (Finding 4) both
  applied in `~/.opencode/opencode.json`.
- **Task**: bounded and concrete — "create this file with this function,
  run it, show the output" — not an open-ended multi-file feature.

Sequence: the orchestrator received an instruction to delegate rather than
do the work itself, spawned a worker session pointed at the local model
(confirmed via `GET /api/v1/sessions/{id}` showing
`model: ai-local-models-cpu/gemma4:e2b`), and the worker correctly wrote and
executed a small Python script and reported the output back, in ~1,000–1,700
seconds of wall time on this hardware under moderate-to-heavy concurrent
system load (dominated by Finding 2's cold-cache prompt-eval cost, not by
generation).

**Recommendation:** route local models to AO's **worker** role for bounded,
single-task delegation — not the **orchestrator** role, which stays
open-ended and long-running by design and is a worse fit for hardware this
constrained regardless of the token-floor finding in Finding 3. Keep a
capable model (cloud or otherwise unconstrained) as the orchestrator that
delegates to local-model workers for the tasks you're comfortable handing to
a `fast`/`coder`-tier model per `ai-local-models`'s routing table.

## What this doesn't establish

- Only one task shape was tested end-to-end (create-a-file-and-run-it). No
  evidence here about longer worker tasks, multi-file edits, or tasks that
  need several tool round-trips within the worker's own turn.
- The system was under variable, sometimes heavy, concurrent load
  (`load average` 6+, swap near-full) during several of these tests from
  this same investigation's own accumulated background sessions — treat the
  specific wall-clock numbers as upper bounds for this hardware, not a clean
  baseline.
- The Vulkan `ErrorDeviceLost` crash is characterized empirically (it
  happens, reliably, around 3,072 tokens) but not root-caused inside
  `ollama`/`ggml-vulkan`; no upstream issue has been filed from this
  investigation.
- No comparison yet against routing AO's local-model sessions through a
  discrete GPU (NVIDIA/CUDA) or Apple Silicon — both are expected to avoid
  Finding 1 entirely and substantially improve Finding 2's throughput, per
  the reasoning in `ai-local-models`'s hardware guidance, but neither has
  been tested against AO directly.
