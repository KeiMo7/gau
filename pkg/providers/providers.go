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
	// WithSources enables provenance mode: per-URL source tracking,
	// cross-run state and the circuit breaker. When false the tool behaves
	// exactly as before this feature existed.
	WithSources bool
	// ProvState is the path of the cross-run provenance state file; "none"
	// disables persistence (the run still reports sources, but no history).
	ProvState string
	// ProvFailLimit is the consecutive-failure threshold after which a
	// provider is skipped with an explicit warning.
	ProvFailLimit uint
	URLScan       URLScan
	OTX           string
}
