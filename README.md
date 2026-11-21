# getallurls (gau)
[![License](https://img.shields.io/badge/license-MIT-_red.svg)](https://opensource.org/licenses/MIT)

getallurls (gau) fetches known URLs from AlienVault's [Open Threat Exchange](https://otx.alienvault.com), the Wayback Machine, Common Crawl, and URLScan for any given domain. Inspired by Tomnomnom's [waybackurls](https://github.com/tomnomnom/waybackurls).

# Resources
- [Usage](#usage)
- [Installation](#installation)
- [Round-to-round diffing (incremental runs)](#round-to-round-diffing-incremental-runs)
- [ohmyzsh note](#ohmyzsh-note)

## Usage:
Examples:

```bash
$ printf example.com | gau
$ cat domains.txt | gau --threads 5
$ gau example.com google.com
$ gau --o example-urls.txt example.com
$ gau --blacklist png,jpg,gif example.com
```

To display the help for the tool use the `-h` flag:

```bash
$ gau -h
```

| Flag | Description | Example |
|------|-------------|---------|
|`--blacklist`| list of extensions to skip | gau --blacklist ttf,woff,svg,png|
|`--config` | Use alternate configuration file (default `$HOME/config.toml` or `%USERPROFILE%\.gau.toml`) | gau --config $HOME/.config/gau.toml|
|`--fc`| list of status codes to filter | gau --fc 404,302 |
|`--from`| fetch urls from date (format: YYYYMM) | gau --from 202101 |
|`--ft`| list of mime-types to filter | gau --ft text/plain|
|`--fp`| remove different parameters of the same endpoint | gau --fp|
|`--history`| directory in which collection rounds are persisted; setting it enables round-to-round diffing | gau --history ./gau-history example.com |
|`--history-import`| import a legacy gau output file (plain text or `--json` lines) as the baseline round into `--history` and exit | gau --history ./gau-history --history-import old-urls.txt example.com |
|`--history-rounds`| number of most recent rounds read for trend aggregation (0 = all rounds) | gau --history ./gau-history --history-rounds 8 example.com |
|`--json`| output as json | gau --json |
|`--mc`| list of status codes to match | gau --mc 200,500 |
|`--mt`| list of mime-types to match |gau --mt text/html,application/json|
|`--o`| filename to write results to | gau --o out.txt |
|`--providers`| list of providers to use (wayback,commoncrawl,otx,urlscan) | gau --providers wayback|
|`--proxy`| http proxy to use (socks5:// or http:// | gau --proxy http://proxy.example.com:8080 |
|`--retries`| retries for HTTP client | gau --retries 10 |
|`--timeout`| timeout (in seconds) for HTTP client | gau --timeout 60 |
|`--subs`| include subdomains of target domain | gau example.com --subs |
|`--threads`| number of workers to spawn | gau example.com --threads |
|`--to`| fetch urls to date (format: YYYYMM) | gau example.com --to 202101 |
|`--verbose`| show verbose output | gau --verbose example.com |
|`--version`| show gau version | gau --version|


## Configuration Files
gau automatically looks for a configuration file at `$HOME/.gau.toml` or`%USERPROFILE%\.gau.toml`. You can point to a different configuration file using the `--config` flag. **If the configuration file is not found, gau will still run with a default configuration, but will output a message to stderr**.

You can specify options and they will be used for every subsequent run of gau. Any options provided via command line flags will override options set in the configuration file.

An example configuration file can be found [here](https://github.com/lc/gau/blob/master/.gau.toml)

The history flags can also be set in the configuration file using the keys
`history`, `history_rounds` and `history_import`.

## Round-to-round diffing (incremental runs)

Weekly external attack-surface reviews mostly re-encounter the same URLs with
the output in a different order. The history feature keeps every collection
round on disk and produces an incremental diff between consecutive rounds, so
the review starts from what actually changed.

It is fully opt-in: without `--history` gau behaves exactly as before, prints
the same output, returns the same exit codes and creates no extra files.

### Enabling it

```bash
# First run: no previous round exists, this round becomes the baseline.
$ gau --history ./gau-history example.com

# Every later run persists a new round and writes a diff against the previous one.
$ gau --history ./gau-history example.com
```

The directory contains one JSON document per round plus one diff report per
non-baseline round:

```
gau-history/
├── round-000001.json          # full normalized snapshot of round 1
├── round-000002.json          # full normalized snapshot of round 2
└── report-000002.json         # incremental diff: round 2 vs round 1
```

Round and report files are written to a temporary name and atomically renamed
into place; a killed process never leaves a half-written round. Rounds
accumulate without limit and are read back oldest-first; `--history-rounds N`
limits how many of the most recent rounds are read for trend aggregation (the
last pair is always diffed).

### What the report contains

| Field | Meaning |
|-------|---------|
| `added` | normalized identities present this round but absent last round |
| `removed` | identities absent this round, where at least one provider that previously supplied them completed successfully |
| `persistent` | identities present in both rounds with the same query-parameter set |
| `changed` | same endpoint (path) with a different query-parameter set; `added_keys`/`removed_keys` name the exact parameter differences |
| `suppressed` | would-be removals whose providers all failed this round — absence is untrusted, so they are **not** counted as removed |
| `added_by_domain` | `added` counts grouped by registrable domain, surfacing rounds where new URLs concentrate under one domain |
| `current_round_failures` | the `(provider, domain)` fetches that errored this round |

### Source failures vs genuine removals

A URL missing this round is ambiguous: the source may have stopped providing
it, or the fetch may simply have failed (connection reset, timeout, malformed
upstream data). Every failed `(provider, domain)` fetch is recorded in the
round. A previously known URL is only placed in `removed` when at least one
provider that previously returned it completed successfully this round;
otherwise it lands in `suppressed` with the failing provider names.

### Normalized identity

Diffs are computed on a normalized identity, so encoding and ordering noise
never produces a fake add/remove pair:

- scheme and host are lower-cased; default ports (`:80`/`:443`) are stripped;
- percent-encoding is canonicalized (`%7E` and `~` are the same endpoint);
- query parameter **names** are sorted and de-duplicated and tracked
  separately from the path identity — parameter *values* are ignored, so the
  same endpoint with a new value stays `persistent`, while a new or dropped
  parameter name is reported in `changed`;
- fragments are ignored; URLs that cannot be parsed fall back to their
  trimmed raw string and still compare deterministically.

### Trends

When three or more rounds are stored, each run additionally logs per-domain
trends (`growing` / `shrinking` / `stable`) with the per-round added, removed
and total counts aligned by round ID, showing whether a class of URLs keeps
expanding or is converging.

### Degradation behavior

- **No previous round** (first run, empty or new directory): the run is
  logged as a baseline; the whole snapshot appears in `added`, no removals are
  possible, and the next run produces the first real diff.
- **Corrupted history file**: the bad file is skipped with a warning that
  names the file and the byte offset where parsing failed; surrounding rounds
  remain readable, so the damage is scoped to that one round.
- **History write failure**: collection and the normal output are unaffected;
  a warning is emitted and the run simply is not persisted.
- **Unreadable history directory**: the feature disables itself for that run
  with a warning; collection still completes.

### Compatibility with older gau output

Older gau versions have no round storage, so the feature cannot diff against
them directly. Legacy output files (one URL per line, or JSON Lines from
`--json`) can be imported as a baseline round:

```bash
$ gau --history ./gau-history --history-import old-urls.txt example.com
```

Imported URLs pass through the same normalization as live data. They carry
the provenance tag `legacy-import` because legacy files record which provider
returned each URL nowhere; consequently an imported URL can only be confirmed
as removed by a fully successful later round — a provider failure can never
silently suppress it.

### Known limitations

- Suppression is keyed on provider/domain failure records. A provider that
  returns HTTP 200 with an empty or truncated body is treated as a successful
  (empty) fetch, matching gau's existing provider semantics.
- Only query-parameter *names* participate in change detection; value changes
  are intentionally ignored to keep the signal high.
- One history directory should be used per logical task/target set; mixing
  unrelated target domains in one directory is supported but makes
  `added_by_domain` noisier.
- History files grow with each round; old rounds may be archived or deleted
  manually (keep the numbering contiguous for simplest trend windows).

### Why this does not reuse `--fp`

`--fp` ("remove different parameters of the same endpoint") is an output-time
de-duplication filter with a different contract: it discards rows from the
printed result and keeps no state between runs. The history feature needs the
full parameter set preserved across runs in order to *report* parameter
changes. The two therefore coexist without touching each other: `--fp`
continues to shape the standard output exactly as before, while `--history`
independently persists the unfiltered normalized set for diffing.

## Installation:
### From source:
```
$ go install github.com/lc/gau/v2/cmd/gau@latest
```
### From github :
```
git clone https://github.com/lc/gau.git; \
cd gau/cmd/gau; \
go build; \
sudo mv gau /usr/local/bin/; \
gau --version;
```
### From binary:
You can download the pre-built binaries from the [releases](https://github.com/lc/gau/releases/) page and then move them into your $PATH.

```bash
$ tar xvf gau_2.0.6_linux_amd64.tar.gz
$ mv gau /usr/bin/gau
```

### From Docker:
You can run gau via docker like so:
```bash
docker run --rm sxcurity/gau:latest --help
```


You can also build a docker image with the following command
```bash
docker build -t gau .
```
and then run it
```bash
docker run gau example.com
```
Bear in mind that piping command (echo "example.com" | gau) will not work with the docker container


## ohmyzsh note:
ohmyzsh's [git plugin](https://github.com/ohmyzsh/ohmyzsh/tree/master/plugins/git) has an alias which maps `gau` to the `git add --update` command. This is problematic, causing a binary conflict between this tool "gau" and the zsh plugin alias "gau" (`git add --update`). There is currently a few workarounds which can be found in this Github [issue](https://github.com/lc/gau/issues/8). 


## Useful?

<a href="http://buymeacoff.ee/cdl" target="_blank"><img src="https://www.buymeacoffee.com/assets/img/custom_images/orange_img.png" alt="Buy Me A Coffee" style="height: 41px !important;width: 174px !important;box-shadow: 0px 3px 2px 0px rgba(190, 190, 190, 0.5) !important;-webkit-box-shadow: 0px 3px 2px 0px rgba(190, 190, 190, 0.5) !important;" ></a>

<a href="https://commoncrawl.org/donate/">Donate to CommonCrawl</a><br>
<a href="https://archive.org/donate">Donate to the InternetArchive</a>
