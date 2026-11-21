# getallurls (gau)
[![License](https://img.shields.io/badge/license-MIT-_red.svg)](https://opensource.org/licenses/MIT)

getallurls (gau) fetches known URLs from AlienVault's [Open Threat Exchange](https://otx.alienvault.com), the Wayback Machine, Common Crawl, and URLScan for any given domain. Inspired by Tomnomnom's [waybackurls](https://github.com/tomnomnom/waybackurls).

# Resources
- [Usage](#usage)
- [Installation](#installation)
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
|`--json`| output as json | gau --json |
|`--with-sources`| provenance mode: keep every URL's providing sources, original spellings and cross-run history | gau --with-sources example.com |
|`--prov-state`| path of the provenance state file (default `$HOME/.gau-prov-state.json`); use `none` to disable persistence | gau --with-sources --prov-state ./state.json example.com |
|`--prov-fail-limit`| consecutive provider failures after which the provider is skipped for the run with a warning (default 3) | gau --with-sources --prov-fail-limit 5 example.com |
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

## URL provenance (source tracking)
By default gau prints one URL per line, deduplicated in arrival order, with no
information about which provider returned it. Pass `--with-sources` to enable
provenance mode; without the flag every behavior, output line and exit code is
identical to older versions.

```bash
$ gau --with-sources example.com
$ gau --with-sources --json example.com
$ gau --with-sources --prov-state ./gau-state.json --o results.tsv example.com
$ printf 'example.com\n' | gau --with-sources --prov-state none
```

What the mode adds:

- **All sources per URL.** Every normalized URL lists every provider that ever
  returned it, plus each provider's original spelling(s) (`raw_forms`:
  case, fragment, percent-encoding, scheme spelling are preserved).
- **First appearance.** `first_seen` is the earliest run in which the URL was
  observed by any source; each source keeps its own `first_seen`/`last_seen`.
- **Source priority by count.** Results are ordered by number of providing
  sources (most sources first), not lexicographically; ties fall back to
  `first_seen`, then URL, so identical inputs produce byte-identical output.
- **Empty vs failed.** A provider that returns no URLs is marked `status=empty`;
  a provider that errors is `status=failed`. The two are never conflated.
- **Circuit breaker.** A provider failing `--prov-fail-limit` consecutive runs
  (default 3) is marked `status=skipped` and not contacted that run, with a
  warning on stderr; one successful run resets the counter.
- **Run-to-run changes.** `added_sources` / `removed_sources` on each record
  show which providers started or stopped providing the URL compared with the
  previous run. Sources that drop out remain in the state file as history.
- **Malformed input.** Individual records that cannot be parsed are skipped and
  counted in `malformed`; they never abort the whole provider. If *every*
  provider fails for a domain, gau exits non-zero with an explicit error and
  writes no output file, instead of printing an empty list.

Text output is tab-separated; lines beginning with `#` carry run and
per-provider status, and each URL line carries the source count, timestamps and
per-source entries (`prov=...|first=...|last=...|raw=...`). `--json` emits the
same data as a structured document (`records[].sources` is a map, never a
pre-joined string).

### Normalization rules
URL equality is defined in exactly one place
(`pkg/provenance/normalize.go`) and applies only in provenance mode:

1. missing scheme is completed as `http://`
2. scheme and host are lower-cased; Unicode hosts are converted to punycode
3. default ports are stripped (`http:80`, `https:443`); other ports kept
4. percent-escape hex digits are upper-cased (`%2f` → `%2F`)
5. fragments are removed (a literal `#...` is client-side state; an encoded
   `%23` is data and is kept)
6. a trailing `.` on the host is trimmed

Everything else is significant and preserved: query order, repeated
parameters, non-default ports, path case and trailing slashes.

### State file, compatibility and known limits
- Cross-run history lives in the JSON state file (default
  `$HOME/.gau-prov-state.json`, override with `--prov-state`, disable with
  `none`). Both the state file and `--o` output are written via temp-file +
  atomic rename, so a crash never leaves a half-written file.
- In a TOML config the same options use hyphen-free keys (viper does not
  alias the two spellings): `withsources`, `provstate`, `provfaillimit`.
  Command-line flags take precedence over config-file values.
- The file carries a schema `version`. State written by a different version is
  rejected with an explicit error; old-version data cannot be read by this
  version — delete the file (history starts fresh) or migrate it to version 1
  (records keyed per domain with `sources`, `first_seen`, `last_seen`,
  `raw_forms`). Output from runs without `--with-sources` contains no source
  data and cannot be backfilled: re-run with the flag to build history.
- The breaker's failure counter is per provider across runs; with
  `--prov-state none` every run starts with a zero counter, so the breaker
  cannot trip. State merges history per domain; running the same state file
  against different domain sets is supported, but `added/removed` always
  compares against the immediately preceding run for that domain.

### Concurrency determinism and failure behavior
Each work item gets its own result channel and a single adapter goroutine, so
a URL can only ever be attributed to the provider that emitted it, regardless
of worker scheduling; shared collector state is guarded by one mutex.

- **Connection reset / timeout mid-stream:** URLs already emitted are kept;
  the provider is marked `failed`, other providers are unaffected, and
  retries still follow the existing `--retries` setting.
- **Malformed upstream record:** the single record is skipped and counted in
  `malformed` (a normalize failure at the collector, or a JSON decode failure
  inside commoncrawl/wayback which now logs and continues); the provider
  stays `ok` if any valid data arrived.
- **Determinism:** all ordering (records, sources, raw forms, statuses) is
  sorted before output, and the health counter uses an explicit rule — any
  success in a run resets it, only a run with no success and at least one
  failure increments it — so identical inputs produce byte-identical output.

### Performance (same-scale input)
Measured on an 8-core x86-64 host, Go benchmark harness (50k URLs, 4
providers). The fetch path is unchanged and dominates wall-clock time: no new
network calls, and the same per-provider worker concurrency and timeout upper
bound apply.

| Step | Legacy | Provenance mode |
|------|--------|-----------------|
| per-URL ingest (normalize + guarded insert) | n/a (string send only) | ~471 ns/URL, 272 B, 5 allocs |
| normalization alone | — | ~384 ns/URL |
| 50k-URL end-of-run merge + sort | — | ~75 ms |
| 50k-URL output render (text) | ~6.9 ms | ~35 ms |
| 50k-URL JSON render | — | ~25 ms |

The only new **synchronization point** is one mutex held briefly per
observation (a map insert; never across I/O). The extra **copies** are the
normalized key string and the retained raw forms — inherent to the feature,
bounded by distinct URL count. In provenance mode output is rendered once at
the end instead of streamed per URL, which trades constant streaming memory
for holding the merged set; use `--prov-state none` if cross-run history is
not needed.

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
