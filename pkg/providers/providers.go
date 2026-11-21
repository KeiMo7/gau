package providers

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/valyala/fasthttp"
)

const Version = `2.2.4`

// Provider is a generic interface for all archive fetchers
type Provider interface {
	Fetch(ctx context.Context, domain string, results chan string) error
	Name() string
}

// Result is a collected URL together with the provenance needed by the
// optional history/diff feature. The Provider interface itself is unchanged:
// the runner attaches the tags while relaying strings from a provider-scoped
// channel onto the shared results channel.
type Result struct {
	URL      string
	Provider string
	Domain   string
}

// Failure records a provider fetch for a domain that ended with an error.
type Failure struct {
	Provider string
	Domain   string
	Error    string
}

type URLScan struct {
	Host   string
	APIKey string
}

type Config struct {
	Threads           uint
	Timeout           uint
	MaxRetries        uint
	IncludeSubdomains bool
	RemoveParameters  bool
	Client            *fasthttp.Client
	Providers         []string
	Blacklist         mapset.Set[string]
	Output            string
	JSON              bool
	URLScan           URLScan
	OTX               string
	// HistoryDir/HistoryRounds/HistoryImport drive the optional round-to-round
	// diff feature. Zero values keep the feature fully disabled.
	HistoryDir    string
	HistoryRounds int
	HistoryImport string
}
