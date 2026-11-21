# Two-round incremental diff example

This is a worked example of the history feature (`--history`) over two
weekly collection rounds against `example.com`. It is generated output, kept
here so the file layout and report shape can be inspected without running the
tool.

## Scenario

- **Round 1** (`round-000001.json`): four URLs returned by wayback/otx.
- **Round 2** (`round-000002.json`): the wayback fetch fails with
  `connection reset by peer`; otx completes and returns evolved data.

## What the diff (`report-000002.json`) concludes

| Bucket | URLs | Why |
|--------|------|-----|
| `added` | `https://example.com/new-post`, `https://api.example.com/v2/orders` | present in round 2, absent in round 1 |
| `persistent` | `https://example.com/home` | identical normalized identity and parameter set in both |
| `changed` | `https://example.com/search` | same path; parameter `lang` was added (`added_keys: ["lang"]`) |
| `suppressed` | `https://example.com/old-page`, `https://api.example.com/v1/users` | their only supplying provider (wayback) failed this round, so the absence is untrusted and they are **not** reported as removed |
| `removed` | *(empty)* | nothing can be confirmed gone while wayback is down |
| `added_by_domain` | `{"example.com": 2}` | both additions group under the same registrable domain |

Note the encoding/normalization effect: `?q=cat&p=1` versus
`?q=dog&p=1&lang=en` on `/search` is a parameter-structure change, not an
add plus a remove, because parameter values are ignored and the path identity
is unchanged.
