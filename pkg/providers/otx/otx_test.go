package otx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lc/gau/v2/pkg/providers"
	"github.com/valyala/fasthttp"
)

type fakeOTX struct {
	mu       sync.Mutex
	requests map[int]int
	handler  func(page, attempt int) (int, string)
}

func (f *fakeOTX) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	f.mu.Lock()
	f.requests[page]++
	attempt := f.requests[page]
	f.mu.Unlock()
	status, body := f.handler(page, attempt)
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	fmt.Fprint(w, body)
}

func otxPage(hasNext bool, urls ...string) string {
	var b strings.Builder
	b.WriteString(`{"has_next":`)
	fmt.Fprintf(&b, "%v", hasNext)
	b.WriteString(`,"url_list":[`)
	for i, u := range urls {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"url":%q}`, u)
	}
	b.WriteString(`]}`)
	return b.String()
}

func newTestClient(t *testing.T, upstream http.Handler, cfg *providers.Config) *Client {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	old := _BaseURL
	setBaseURL(srv.URL + "/")
	t.Cleanup(func() { setBaseURL(old) })
	if cfg == nil {
		cfg = &providers.Config{Client: &fasthttp.Client{}, Timeout: 5, MaxRetries: 2, MaxPages: 50}
	}
	if cfg.Client == nil {
		cfg.Client = &fasthttp.Client{}
	}
	return New(cfg)
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

// 跨页去重 + 确定性输出：同一批条目重复出现只保留一条，连跑两次输出一致。
func TestOTXDedupeDeterministic(t *testing.T) {
	up := &fakeOTX{requests: map[int]int{}}
	up.handler = func(page, _ int) (int, string) {
		switch page {
		case 1:
			return 200, otxPage(true, "http://x.example.com/1", "http://x.example.com/2")
		case 2:
			// repeats page 1 (e.g. after a retry upstream) plus one new row
			return 200, otxPage(false, "http://x.example.com/2/", "http://x.example.com/1", "http://x.example.com/3")
		default:
			return 200, otxPage(false)
		}
	}
	c := newTestClient(t, up, nil)

	first, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := runFetch(t, c, "example.com")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3 unique rows, got %d: %v", len(first), first)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("outputs differ:\nfirst:  %v\nsecond: %v", first, second)
		}
	}
}

// 某页失败后无法知道是否还有下一页：停止翻页、保留已取行、报告不完整。
func TestOTXPageFailure(t *testing.T) {
	up := &fakeOTX{requests: map[int]int{}}
	up.handler = func(page, _ int) (int, string) {
		if page == 1 {
			return 200, otxPage(true, "http://y.example.com/1")
		}
		return 500, ""
	}
	c := newTestClient(t, up, &providers.Config{Client: &fasthttp.Client{}, Timeout: 5, MaxRetries: 1, MaxPages: 50})

	out, err := runFetch(t, c, "example.com")
	if err == nil {
		t.Fatal("expected incomplete-fetch error")
	}
	if !strings.Contains(err.Error(), "missing page(s) [2]") {
		t.Fatalf("error should name the missing page, got: %v", err)
	}
	if len(out) != 1 || out[0] != "http://y.example.com/1" {
		t.Fatalf("rows from the healthy page must be kept, got: %v", out)
	}
}

// 没有结果与请求失败分开：has_next=false 且空列表是完整轮次。
func TestOTXNoResults(t *testing.T) {
	up := &fakeOTX{requests: map[int]int{}}
	up.handler = func(page, _ int) (int, string) {
		return 200, otxPage(false)
	}
	c := newTestClient(t, up, nil)

	out, err := runFetch(t, c, "empty.example.com")
	if err != nil {
		t.Fatalf("empty result set must not be a failure: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no rows, got %v", out)
	}
}
