#!/usr/bin/env python3
"""Regenerate opencode-fixture.db. Run from this directory: python3 make-opencode-fixture.py"""
import json, os, sqlite3

OUT = os.path.join(os.path.dirname(__file__), "opencode-fixture.db")
if os.path.exists(OUT):
    os.remove(OUT)

db = sqlite3.connect(OUT)
db.executescript("""
CREATE TABLE session (
  id text PRIMARY KEY, project_id text NOT NULL, parent_id text, slug text NOT NULL,
  directory text NOT NULL, title text NOT NULL, version text NOT NULL,
  summary_files integer, summary_additions integer, summary_deletions integer,
  time_created integer NOT NULL, time_updated integer NOT NULL, time_compacting integer,
  agent text, model text, cost real DEFAULT 0 NOT NULL,
  tokens_input integer DEFAULT 0 NOT NULL, tokens_output integer DEFAULT 0 NOT NULL,
  tokens_reasoning integer DEFAULT 0 NOT NULL, tokens_cache_read integer DEFAULT 0 NOT NULL,
  tokens_cache_write integer DEFAULT 0 NOT NULL
);
CREATE TABLE session_message (
  id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL,
  time_created integer NOT NULL, time_updated integer NOT NULL,
  data text NOT NULL, seq integer NOT NULL
);
CREATE TABLE part (
  id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL,
  time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL
);
""")

# A paid session with cache usage, on a real cloud model.
db.execute(
    "INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
    ("ses_paid", "proj_1", None, "paid", "/tmp/wt/paid", "Paid session", "1.0",
     3, 120, 14, 1_773_336_000_000, 1_773_336_900_000, None,
     "ao-paid", json.dumps({"id": "claude-sonnet-4-6", "providerID": "anthropic", "variant": "default"}),
     2.84, 184_000, 23_000, 0, 412_000, 9_000),
)
# A local-model session: cost is a KNOWN zero, not missing.
db.execute(
    "INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
    ("ses_local", "proj_1", None, "local", "/tmp/wt/local", "Local session", "1.0",
     0, 0, 0, 1_773_337_000_000, 1_773_337_100_000, 1_773_337_090_000,
     "ao-local", json.dumps({"id": "gemma4:e2b", "providerID": "ai-local-models-cpu", "variant": "default"}),
     0.0, 1_130, 778, 0, 19_990, 0),
)
# A child session, to prove parent_id round-trips.
db.execute(
    "INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
    ("ses_child", "proj_1", "ses_paid", "child", "/tmp/wt/paid", "Child", "1.0",
     0, 0, 0, 1_773_336_500_000, 1_773_336_600_000, None,
     "ao-paid", json.dumps({"id": "claude-haiku-4-5", "providerID": "anthropic", "variant": "default"}),
     0.05, 2_000, 400, 0, 0, 0),
)

parts = [
    # seq 1: a tool call WITH timing.
    ("ses_paid", 1, {"type": "tool", "callID": "call_a", "tool": "bash",
                     "state": {"status": "completed", "title": "npm test",
                               "time": {"start": 1_773_336_859_125, "end": 1_773_336_863_340}}}),
    # seq 2: a tool call WITHOUT timing — must stay unknown, not zero.
    ("ses_paid", 2, {"type": "tool", "callID": "call_b", "tool": "read",
                     "state": {"status": "completed", "title": "read file"}}),
    # seq 3: a failed tool call.
    ("ses_paid", 3, {"type": "tool", "callID": "call_c", "tool": "bash",
                     "state": {"status": "error", "title": "exit 1",
                               "time": {"start": 1_773_336_870_000, "end": 1_773_336_871_000}}}),
    # seq 4: per-step token vector.
    ("ses_paid", 4, {"type": "step-finish", "reason": "tool-calls", "cost": 0,
                     "tokens": {"total": 16958, "input": 16881, "output": 77,
                                "reasoning": 0, "cache": {"read": 0, "write": 0}}}),
    # seq 5: an automatic overflow compaction.
    ("ses_local", 5, {"type": "compaction", "auto": True, "overflow": True}),
]
for index, (session_id, seq, payload) in enumerate(parts):
    message_id = f"msg_{seq}"
    db.execute(
        "INSERT OR IGNORE INTO session_message VALUES (?,?,?,?,?,?,?)",
        (message_id, session_id, "assistant", 1_773_336_000_000 + seq,
         1_773_336_000_000 + seq, "{}", seq),
    )
    db.execute(
        "INSERT INTO part VALUES (?,?,?,?,?,?)",
        (f"prt_{index}", message_id, session_id,
         1_773_336_000_000 + seq, 1_773_336_000_000 + seq, json.dumps(payload)),
    )

db.commit()
db.close()
print("wrote", OUT)
