---
name: grok-search
description: Real-time web research and live synthesis with sources. Use when the question depends on freshness, community chatter, X/Twitter dynamics, breaking updates, or broad multi-source summaries. Prefer this over source-first search when you need fast situational awareness. Prefer exa-search instead for official docs, API references, pricing pages, canonical source retrieval, or direct page-text extraction.
---

# Grok Search

Use Grok Search for **freshness-first research**.

Prefer it when the task is about:
- breaking news
- X/Twitter chatter
- fast-moving narratives
- “what are people saying now?”
- quick multi-source synthesis
- comparing official claims vs community discussion

Do **not** default to Grok Search for:
- official docs lookup
- API/reference pages
- pricing / plan details
- direct page text extraction
- canonical source retrieval

For those, prefer `exa-search`.

## Workflow

1. Use `--mode news` for fresh updates.
2. Use `--mode social` for X/Twitter and discourse-heavy prompts.
3. Use `--mode research` for broad multi-source synthesis.
4. Use `--mode docs-compare` when you want official claims plus community interpretation.
5. Use `--plain` for human-readable terminal output.
6. Use `--profile <id>` when testing one key or diagnosing failover.
7. Use `--ignore-cooldown` only when you intentionally want to override a temporary cooldown and force another try.

## Config

Preferred key resolution order:
1. `--api-key`
2. `GROK_API_KEY`
3. `GROK_API_KEYS` (comma-separated)
4. `config.local.json`
5. `config.json`
6. `~/.codex/config/grok-search.json`

Recommended setup for this workspace: keep the real key(s) inside the skill folder in `config.local.json` so the whole skill can be backed up and moved as one directory.

### Single key

```json
{
  "profiles": [
    { "id": "main", "api_key": "YOUR_GROK_API_KEY" }
  ]
}
```

### Multiple keys with auto failover

```json
{
  "profiles": [
    { "id": "main", "api_key": "KEY_1" },
    { "id": "backup-1", "api_key": "KEY_2" },
    { "id": "backup-2", "api_key": "KEY_3" }
  ],
  "base_url": "https://your-grok-endpoint.example",
  "model": "grok-4.1-fast",
  "timeout_seconds": 120,
  "extra_body": {},
  "extra_headers": {},
  "cooldown": {
    "enabled": true,
    "state_file": "runtime/cooldowns.json",
    "default_minutes": 15,
    "rate_limit_minutes": 20,
    "quota_minutes": 60,
    "auth_minutes": 360
  }
}
```

Failover behavior:
- profiles are tried in order
- 401 / 403 / 429 and quota / billing / rate-limit / token-unavailable style errors automatically move to the next key
- output includes `profileId` and `attempts`

Cooldown behavior:
- failover-worthy failures place the profile into temporary cooldown
- cooldown state lives in `runtime/cooldowns.json` by default
- later runs will skip cooling profiles instead of hammering the same failing key again
- use `--ignore-cooldown` if you intentionally want to force a retry

## Commands

### General live research
```bash
python3 scripts/grok_search.py --query "What changed in OpenClaw recently?"
```

### Breaking-news style lookup
```bash
python3 scripts/grok_search.py --mode news --query "Latest OpenClaw Telegram streaming changes"
```

### Social/discourse lookup
```bash
python3 scripts/grok_search.py --mode social --query "What are people saying about OpenClaw on X?"
```

### Multi-source synthesis
```bash
python3 scripts/grok_search.py --mode research --query "Summarize recent discussion around OpenClaw model failover"
```

### Official docs vs community interpretation
```bash
python3 scripts/grok_search.py --mode docs-compare --query "Compare OpenClaw official docs and community discussion on Telegram streaming"
```

### Force one profile
```bash
python3 scripts/grok_search.py --query "OpenClaw Telegram streaming" --profile main
```

## Notes

- This skill is optimized for freshness and breadth, not canonical-source purity.
- `docs-compare` now explicitly separates official facts from community interpretation.
- If the answer must come from official docs, switch to `exa-search`.
- Use `references/query-recipes.md` for reusable query patterns.
