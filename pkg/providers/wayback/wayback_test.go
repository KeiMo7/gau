package wayback

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lc/gau/v2/pkg/providers"
	"github.com/valyala/fasthttp"
)

// fakeUpstream is a local stand-in for the wayback CDX API. Each test
// registers per-page handlers; the upstream counts requests per page so
// tests can assert that only the failed page was retried.
type fakeUpstream struct {
	mu       sync.Mutex
	requests map[uint]int
	handler  func(page uint, attempt int) (int, string)
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))

	f.mu.Lock()
	f.requests[uint(page)]++
	attempt := f.requests[uint(page)]
	f.mu.Unlock()

	status, body := f.handler(uint(page), attempt)
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

func (f *fakeUpstream) pageRequests(page uint) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[page]
}

// wbPage renders one CDX page: a header row followed by data rows.
func wbPage(rows ...string) string {
	var b strings.Builder
	b.WriteString(`[["original"]`)
	for _, r := range rows {
		fmt.Fprintf(&b, `,[%q]`, r)
	}
	b.WriteString(`]`)
	return b.String()
}

// wbPageNoHeader renders one CDX page without a header row.
func wbPageNoHeader(rows ...string) string {
	var b strings.Builder
	b.WriteString(`[`)
	for i, r := range rows {
		if i > 0 {
			b.WriteString(`,`)
		}
		fmt.Fprintf(&b, `[%q]`, r)
	}
	b.WriteString(`]`)
	return b.String()
}

func newTestClient(t *testing.T, upstream http.Handler, cfg *providers.Config) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	old := _BaseURL
	setBaseURL(srv.URL)
	t.Cleanup(func() { setBaseURL(old) })
	if cfg == nil {
		cfg = &providers.Config{
			Client:     &fasthttp.Client{},
			Timeout:    5,
			MaxRetries: 2,
			MaxPages:   50,
		}
	}
	if cfg.Client == nil {
		cfg.Client = &fasthttp.Client{}
	}
	return New(cfg, providers.Filters{}), srv
}

func runFetch(t *testing.T, c *Client, domain string) ([]string, error) {
	t.Helper()
	results := make(chan string, 100000)
	err := c.Fetch(context.Background(), domain, results)
	close(results)
	var out []string
	for r := range results {
		out = append(out, r)
	}
	return out, err
}

func assertNoDupes(t *testing.T, out []string) {
	t.Helper()
	seen := map[string]int{}
	for _, u := range out {
		seen[u]++
	}
	for u, n := range seen {
		if n > 1 {
			t.Errorf("url %q appears %d times in output", u, n)
		}
	}
}

func assertContains(t *testing.T, out []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, u := range out {
		set[u] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("expected %q in output, missing (got %d rows)", w, len(out))
		}
	}
}

// 固定页：三页数据 + 空末页。断言不丢行、不重复、首页第一条在结果里。
func TestFixedPagesEmptyLastPage(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	pages := map[uint]string{
		0: wbPage("http://a.example.com/1", "http://a.example.com/2"),
		1: wbPage("http://a.example.com/3", "http://a.example.com/4"),
		2: wbPage("http://a.example.com/5"),
		3: `[]`, // empty last page
	}
	up.handler = func(page uint, _ int) (int, string) {
		body, ok := pages[page]
		if !ok {
			return 200, `[]`
		}
		return 200, body
	}
	c, _ := newTestClient(t, up, nil)

	out, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 5 {
		t.Fatalf("expected 5 rows, got %d: %v", len(out), out)
	}
	assertNoDupes(t, out)
	// first row of the first page and last row of the last data page
	assertContains(t, out, "http://a.example.com/1", "http://a.example.com/5")
	if up.pageRequests(4) != 0 {
		t.Fatalf("pagination should have stopped at the empty page, got page 4 requested")
	}
}

// 乱序页：上游两次运行页内顺序不同，输出必须完全一致。
func TestShuffledPagesDeterministicOutput(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	var callMu sync.Mutex
	call := 0
	up.handler = func(page uint, _ int) (int, string) {
		callMu.Lock()
		call++
		n := call
		callMu.Unlock()
		switch page {
		case 0:
			if n%2 == 0 {
				return 200, wbPage("http://b.example.com/1", "http://b.example.com/2", "http://b.example.com/3")
			}
			return 200, wbPage("http://b.example.com/3", "http://b.example.com/1", "http://b.example.com/2")
		case 1:
			if n%2 == 0 {
				return 200, wbPage("http://b.example.com/4", "http://b.example.com/5")
			}
			return 200, wbPage("http://b.example.com/5", "http://b.example.com/4")
		default:
			return 200, `[]`
		}
	}
	c, _ := newTestClient(t, up, nil)

	first, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("run lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("run outputs differ at %d: %q vs %q\nfirst:  %v\nsecond: %v", i, first[i], second[i], first, second)
		}
	}
	assertNoDupes(t, first)
}

// 重复页：下一页重复上一页的尾巴，跨页去重后条数不变。
func TestDuplicateTailAcrossPages(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	up.handler = func(page uint, _ int) (int, string) {
		switch page {
		case 0:
			return 200, wbPage("http://c.example.com/1", "http://c.example.com/2", "http://c.example.com/3")
		case 1:
			// repeats the tail of page 0
			return 200, wbPage("http://c.example.com/3", "http://c.example.com/4")
		case 2:
			// repeats page 1 entirely
			return 200, wbPage("http://c.example.com/3", "http://c.example.com/4")
		default:
			return 200, `[]`
		}
	}
	c, _ := newTestClient(t, up, nil)

	out, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("expected 4 unique rows, got %d: %v", len(out), out)
	}
	assertNoDupes(t, out)
}

// 等价键：同一地址的不同写法（末尾斜杠、默认端口、片段、大小写）只保留一条。
func TestNormalizationVariants(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	up.handler = func(page uint, _ int) (int, string) {
		switch page {
		case 0:
			return 200, wbPage(
				"http://d.example.com/path",
				"http://d.example.com/path/",
				"http://d.example.com:80/path",
				"http://d.example.com/path#section",
				"HTTP://D.EXAMPLE.COM/path",
			)
		case 1:
			return 200, wbPage(
				"https://d.example.com:443/secure",
				"https://d.example.com/secure/",
				"http://d.example.com:8080/path", // non-default port: distinct
				"http://d.example.com/other?a=1&b=2",
			)
		default:
			return 200, `[]`
		}
	}
	c, _ := newTestClient(t, up, nil)

	out, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 5 spellings of /path collapse to 1; /secure pair collapses to 1;
	// :8080 and the query URL stay distinct.
	if len(out) != 4 {
		t.Fatalf("expected 4 rows after normalization, got %d: %v", len(out), out)
	}
	assertNoDupes(t, out)
	assertContains(t, out, "http://d.example.com:8080/path", "http://d.example.com/other?a=1&b=2")
}

// 无表头页：上游不送表头行时，每页第一条也必须保留。
func TestFirstRowPreservedWithoutHeader(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	up.handler = func(page uint, _ int) (int, string) {
		switch page {
		case 0:
			return 200, wbPageNoHeader("http://e.example.com/first", "http://e.example.com/second")
		default:
			return 200, `[]`
		}
	}
	c, _ := newTestClient(t, up, nil)

	out, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, out, "http://e.example.com/first", "http://e.example.com/second")
}

// 中间页先失败再成功：只重试该页，结果完整。
func TestMiddlePageFailsThenSucceeds(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	up.handler = func(page uint, attempt int) (int, string) {
		switch page {
		case 0:
			return 200, wbPage("http://f.example.com/1")
		case 1:
			if attempt == 1 {
				return 500, ""
			}
			return 200, wbPage("http://f.example.com/2")
		case 2:
			return 200, wbPage("http://f.example.com/3")
		default:
			return 200, `[]`
		}
	}
	c, _ := newTestClient(t, up, nil)

	out, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("expected complete fetch after retry, got: %v", err)
	}
	assertContains(t, out, "http://f.example.com/1", "http://f.example.com/2", "http://f.example.com/3")
	if got := up.pageRequests(1); got != 2 {
		t.Fatalf("page 1 should have been requested exactly twice, got %d", got)
	}
	if got := up.pageRequests(0); got != 1 {
		t.Fatalf("page 0 should have been requested once, got %d", got)
	}
	if got := up.pageRequests(2); got != 1 {
		t.Fatalf("page 2 should have been requested once, got %d", got)
	}
}

// 某页重试后仍失败：来源标记不完整并指明缺页，其它页的行照常输出。
func TestPagePermanentlyFails(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	up.handler = func(page uint, _ int) (int, string) {
		switch page {
		case 0:
			return 200, wbPage("http://g.example.com/1")
		case 1:
			return 500, ""
		case 2:
			return 200, wbPage("http://g.example.com/3")
		default:
			return 200, `[]`
		}
	}
	cfg := &providers.Config{Client: &fasthttp.Client{}, Timeout: 5, MaxRetries: 1, MaxPages: 50}
	c, _ := newTestClient(t, up, cfg)

	out, err := runFetch(t, c, "example.com")
	if err == nil {
		t.Fatal("expected an incomplete-fetch error")
	}
	if !strings.Contains(err.Error(), "missing page(s) [1]") {
		t.Fatalf("error should name the missing page, got: %v", err)
	}
	// rows from the healthy pages are still emitted
	assertContains(t, out, "http://g.example.com/1", "http://g.example.com/3")
	if got := up.pageRequests(1); got != 2 { // 1 try + 1 retry
		t.Fatalf("page 1 should have been retried once, got %d requests", got)
	}
}

// 畸形数据：该页判失败，其它页照常保留。
func TestMalformedPage(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	up.handler = func(page uint, _ int) (int, string) {
		switch page {
		case 0:
			return 200, wbPage("http://h.example.com/1")
		case 1:
			return 200, `this is not json`
		case 2:
			return 200, wbPage("http://h.example.com/3")
		default:
			return 200, `[]`
		}
	}
	c, _ := newTestClient(t, up, nil)

	out, err := runFetch(t, c, "example.com")
	if err == nil {
		t.Fatal("expected an incomplete-fetch error")
	}
	if !strings.Contains(err.Error(), "missing page(s) [1]") {
		t.Fatalf("error should name the malformed page, got: %v", err)
	}
	assertContains(t, out, "http://h.example.com/1", "http://h.example.com/3")
}

// 确实没有结果（首页即空）与请求失败必须区分：前者完整、无错误。
func TestNoResultsIsNotAFailure(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	up.handler = func(page uint, _ int) (int, string) {
		return 200, `[]`
	}
	c, _ := newTestClient(t, up, nil)

	out, err := runFetch(t, c, "empty.example.com")
	if err != nil {
		t.Fatalf("empty result set must not be reported as failure: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no rows, got %v", out)
	}
}

// 分页没有终止条件：到达硬上限停止，并明确说明是上限停止。
func TestPageCap(t *testing.T) {
	up := &fakeUpstream{requests: map[uint]int{}}
	up.handler = func(page uint, _ int) (int, string) {
		// never terminates: every page has data
		return 200, wbPage(fmt.Sprintf("http://i.example.com/%d", page))
	}
	cfg := &providers.Config{Client: &fasthttp.Client{}, Timeout: 5, MaxRetries: 0, MaxPages: 3}
	c, _ := newTestClient(t, up, cfg)

	out, err := runFetch(t, c, "example.com")
	if err == nil {
		t.Fatal("expected an incomplete-fetch error at the page cap")
	}
	if !strings.Contains(err.Error(), "page cap") {
		t.Fatalf("error should state the fetch stopped at the page cap, got: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected rows from 3 pages, got %d: %v", len(out), out)
	}
	if got := up.pageRequests(3); got != 0 {
		t.Fatalf("no page beyond the cap should be requested, page 3 got %d requests", got)
	}
}

// 子域开关语义不变。
func TestIncludeSubdomains(t *testing.T) {
	cfg := &providers.Config{IncludeSubdomains: true}
	c := New(cfg, providers.Filters{})
	u := c.formatURL("example.com", 0)
	if !strings.Contains(u, "url=*.example.com/*") {
		t.Fatalf("subdomain wildcard missing: %s", u)
	}

	cfg.IncludeSubdomains = false
	c = New(cfg, providers.Filters{})
	u = c.formatURL("example.com", 0)
	if strings.Contains(u, "*.") {
		t.Fatalf("unexpected wildcard without subs flag: %s", u)
	}
	if !strings.Contains(u, "url=example.com/*") {
		t.Fatalf("domain missing from url: %s", u)
	}
}

// 过滤参数语义不变：仍然附加到请求上。
func TestFiltersStillApplied(t *testing.T) {
	filters := providers.Filters{MatchStatusCodes: []string{"200"}, From: "202001"}
	c := New(&providers.Config{}, filters)
	u := c.formatURL("example.com", 0)
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("from") != "202001" {
		t.Fatalf("from filter missing: %s", u)
	}
	found := false
	for _, f := range q["filter"] {
		if f == "statuscode:200" {
			found = true
		}
	}
	if !found {
		t.Fatalf("statuscode filter missing: %s", u)
	}
}
