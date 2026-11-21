# Before/after output comparison for the same input

Feature flag off (default, unchanged behavior) versus on. The standard
collection output is identical in both cases; the difference appears only in
the new history artifacts and the summary lines on stderr.

## Input

```
$ printf 'example.com\n' | gau [flags]
```

## Before / flag off — existing behavior preserved

stdout (exactly as before; order and filtering unchanged):

```
https://example.com/home
https://example.com/search?q=cat&p=1
https://example.com/old-page
https://api.example.com/v1/users?id=1
```

Files created in the working directory: **none**. Exit code: `0`.

## After / flag on — `--history ./gau-history`

stdout: **byte-identical to the flag-off run** (same URLs, same order).

Additional stderr summary (warnings, so stdout stays clean for piping):

```
level=warning msg="history: persisted round 1 (4 URLs, 0 provider failures) to ./gau-history"
level=warning msg="history: no previous round found — round 1 is the baseline; only a full snapshot is available, re-run after the next collection to get an incremental diff"
```

Additional files (only when the flag is set):

```
gau-history/round-000001.json     # normalized full snapshot, atomic write
```

On the second run, one more line and one more file appear — this is the
field-level difference the feature adds; the standard output still does not
change:

```
level=warning msg="history: round 2 vs 1 — added=2 removed=0 persistent=1 changed=1 suppressed(down-source)=2"
```

```
gau-history/round-000002.json
gau-history/report-000002.json    # added / removed / persistent / changed / suppressed / added_by_domain
```

## Where the difference lives

| Concern | Flag off (before) | Flag on (after) |
|---------|-------------------|-----------------|
| stdout content / order | as today | unchanged |
| process exit code | as today | unchanged; history write/read failures only warn |
| extra files | none | `round-N.json` (+ `report-N.json`) inside the chosen directory |
| channel payload | `string` | `providers.Result{URL, Provider, Domain}` — the legacy writer consumes only `.URL` |
| per-URL CPU work | none extra | one `Normalize` + locked map merge, ~0.5 µs/URL |
| new synchronization points | none | one mutex inside the collector; no new network synchronization, no extra copy of the output stream |

## Field-level view of a change

The same endpoint with an evolved query string is reported as a parameter
change rather than an add/remove pair. Given:

- round 1: `https://example.com/search?q=cat&p=1`
- round 2: `https://example.com/search?q=dog&p=1&lang=en`

the report's `changed` entry is:

```json
{
  "id": "https://example.com/search",
  "domain": "example.com",
  "sample": "https://example.com/search?q=dog&p=1&lang=en",
  "added_keys": ["lang"],
  "removed_keys": null
}
```

The value change `q=cat` → `q=dog` is intentionally invisible (values are not
part of the identity); the new parameter name `lang` is the reported diff.
