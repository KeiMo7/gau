package wayback

import (
	"context"
	"errors"
	"fmt"

	jsoniter "github.com/json-iterator/go"
	"github.com/lc/gau/v2/pkg/httpclient"
	"github.com/lc/gau/v2/pkg/providers"
	"github.com/sirupsen/logrus"
)

const (
	Name = "wayback"
)

// verify interface compliance
var _ providers.Provider = (*Client)(nil)

// Client is the structure that holds the WaybackFilters and the Client's configuration
type Client struct {
	filters providers.Filters
	config  *providers.Config
}

func New(config *providers.Config, filters providers.Filters) *Client {
	return &Client{filters, config}
}

func (c *Client) Name() string {
	return Name
}

// waybackResult holds the response from the wayback API
type waybackResult [][]string

// Fetch fetches all urls for a given domain and sends them to a channel.
//
// Pages are fetched one at a time and each page is retried on its own
// (a failed page never re-runs the whole source). Rows are
// deduplicated across pages by a normalized equivalence key and are
// emitted in a deterministic, sorted order once pagination ends, so
// the output does not depend on the upstream's page ordering. If a
// page cannot be fetched or decoded, the source is marked incomplete
// and the missing page is reported, but the rows from the other pages
// are still emitted.
func (c *Client) Fetch(ctx context.Context, domain string, results chan string) error {
	col := providers.NewPageCollector(Name, domain)
	maxPages := c.config.MaxPages
	if maxPages == 0 {
		maxPages = providers.DefaultMaxPages
	}

	var maxPageRows int
	for page := uint(0); ; page++ {
		select {
		case <-ctx.Done():
			col.Report()
			return nil
		default:
		}

		if page >= maxPages {
			col.MarkPageCap()
			break
		}

		logrus.WithFields(logrus.Fields{"provider": Name, "page": page}).Infof("fetching %s", domain)
		apiURL := c.formatURL(domain, page)
		// make HTTP request; MakeRequest retries this page only
		resp, err := httpclient.MakeRequest(c.config.Client, apiURL, c.config.MaxRetries, c.config.Timeout)
		if err != nil {
			if errors.Is(err, httpclient.ErrBadRequest) {
				// wayback answers 400 when the requested page is out of
				// range or the domain has no captures: end of pagination.
				break
			}
			col.FailPage(page, err)
			continue
		}
		var result waybackResult
		if err = jsoniter.Unmarshal(resp, &result); err != nil {
			col.FailPage(page, fmt.Errorf("malformed response: %w", err))
			continue
		}

		// an empty page means there are no more results; rows from
		// earlier pages are kept.
		if len(result) == 0 {
			break
		}

		rows := make([]string, 0, len(result))
		for i, entry := range result {
			if len(entry) == 0 || entry[0] == "" {
				continue
			}
			// Skip the first row only when it is actually the header,
			// so the first data row of the first page is never lost.
			if i == 0 && entry[0] == "original" {
				continue
			}
			rows = append(rows, entry[0])
		}
		if len(rows) == 0 {
			break
		}

		// A non-final page that is shorter than a previous one may have
		// been truncated upstream; keep the rows but say so.
		if len(rows) < maxPageRows {
			logrus.WithFields(logrus.Fields{"provider": Name, "page": page}).
				Warnf("page returned %d rows, fewer than a previous page (%d); response may be truncated", len(rows), maxPageRows)
		}
		if len(rows) > maxPageRows {
			maxPageRows = len(rows)
		}

		col.AddPage(page, rows)
	}

	for _, u := range col.Sorted() {
		select {
		case <-ctx.Done():
			col.Report()
			return nil
		case results <- u:
		}
	}

	col.Report()
	if !col.Complete() {
		return fmt.Errorf("incomplete fetch for %s: %s", domain, col.IncompleteReason())
	}
	return nil
}

// formatUrl returns a formatted URL for the Wayback API
func (c *Client) formatURL(domain string, page uint) string {
	if c.config.IncludeSubdomains {
		domain = "*." + domain
	}
	filterParams := c.filters.GetParameters(true)
	return fmt.Sprintf(
		"%s?url=%s/*&output=json&collapse=urlkey&fl=original&pageSize=100&page=%d",
		_BaseURL, domain, page,
	) + filterParams
}

var _BaseURL = "https://web.archive.org/cdx/search/cdx"

func setBaseURL(baseURL string) {
	_BaseURL = baseURL
}
