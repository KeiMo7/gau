package flags

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/lc/gau/v2/pkg/providers"
	"github.com/lynxsecurity/pflag"
	"github.com/lynxsecurity/viper"
	log "github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
)

type URLScanConfig struct {
	Host   string `mapstructure:"host"`
	APIKey string `mapstructure:"apikey"`
}

type Config struct {
	Filters           providers.Filters `mapstructure:"filters"`
	Proxy             string            `mapstructure:"proxy"`
	Threads           uint              `mapstructure:"threads"`
	Timeout           uint              `mapstructure:"timeout"`
	Verbose           bool              `mapstructure:"verbose"`
	MaxRetries        uint              `mapstructure:"retries"`
	IncludeSubdomains bool              `mapstructure:"subdomains"`
	RemoveParameters  bool              `mapstructure:"parameters"`
	Providers         []string          `mapstructure:"providers"`
	Blacklist         []string          `mapstructure:"blacklist"`
	JSON              bool              `mapstructure:"json"`
	WithSources       bool              `mapstructure:"withsources"`
	ProvState         string            `mapstructure:"provstate"`
	ProvFailLimit     uint              `mapstructure:"provfaillimit"`
	URLScan           URLScanConfig     `mapstructure:"urlscan"`
	OTX               string            `mapstructure:"otx"`
	Outfile           string            // output file to write to
}

func (c *Config) ProviderConfig() (*providers.Config, error) {
	var dialer fasthttp.DialFunc

	if c.Proxy != "" {
		parse, err := url.Parse(c.Proxy)
		if err != nil {
			return nil, fmt.Errorf("proxy url: %v", err)
		}
		switch parse.Scheme {
		case "http":
			dialer = fasthttpproxy.FasthttpHTTPDialer(strings.ReplaceAll(c.Proxy, "http://", ""))
		case "socks5":
			dialer = fasthttpproxy.FasthttpSocksDialer(c.Proxy)
		default:
			return nil, fmt.Errorf("unsupported proxy scheme: %s", parse.Scheme)
		}
	}

	pc := &providers.Config{
		Threads:           c.Threads,
		Timeout:           c.Timeout,
		MaxRetries:        c.MaxRetries,
		IncludeSubdomains: c.IncludeSubdomains,
		RemoveParameters:  c.RemoveParameters,
		Client: &fasthttp.Client{
			TLSConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			Dial: dialer,
		},
		Providers:     c.Providers,
		Output:        c.Outfile,
		JSON:          c.JSON,
		WithSources:   c.WithSources,
		ProvState:     c.ProvState,
		ProvFailLimit: c.ProvFailLimit,
		URLScan: providers.URLScan{
			Host:   c.URLScan.Host,
			APIKey: c.URLScan.APIKey,
		},
		OTX: c.OTX,
	}

	log.SetLevel(log.ErrorLevel)
	if c.Verbose {
		log.SetLevel(log.InfoLevel)
	}
	pc.Blacklist = mapset.NewThreadUnsafeSet(c.Blacklist...)
	pc.Blacklist.Add("")
	return pc, nil
}

type Options struct {
	viper *viper.Viper
}

func New() *Options {
	v := viper.New()

	pflag.String("o", "", "filename to write results to")
	pflag.String("config", "", "location of config file (default $HOME/.gau.toml or %USERPROFILE%\\.gau.toml)")
	pflag.Uint("threads", 1, "number of workers to spawn")
	pflag.Uint("timeout", 45, "timeout (in seconds) for HTTP client")
	pflag.Uint("retries", 0, "retries for HTTP client")
	pflag.String("proxy", "", "http proxy to use")
	pflag.StringSlice("blacklist", []string{}, "list of extensions to skip")
	pflag.StringSlice("providers", []string{}, "list of providers to use (wayback,commoncrawl,otx,urlscan)")
	pflag.Bool("subs", false, "include subdomains of target domain")
	pflag.Bool("fp", false, "remove different parameters of the same endpoint")
	pflag.Bool("verbose", false, "show verbose output")
	pflag.Bool("json", false, "output as json")
	pflag.Bool("with-sources", false, "provenance mode: retain every URL's providing sources, raw forms and cross-run history (implies cross-run state via --prov-state)")
	pflag.String("prov-state", "", "path of the provenance state file (default $HOME/.gau-prov-state.json); use 'none' to disable persistence")
	pflag.Uint("prov-fail-limit", 3, "consecutive provider failures after which the provider is skipped for the run with a warning")

	// filter flags
	pflag.StringSlice("mc", []string{}, "list of status codes to match")
	pflag.StringSlice("fc", []string{}, "list of status codes to filter")
	pflag.StringSlice("mt", []string{}, "list of mime-types to match")
	pflag.StringSlice("ft", []string{}, "list of mime-types to filter")
	pflag.String("from", "", "fetch urls from date (format: YYYYMM)")
	pflag.String("to", "", "fetch urls to date (format: YYYYMM)")
	pflag.Bool("version", false, "show gau version")

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	if err := v.BindPFlags(pflag.CommandLine); err != nil {
		log.Fatal(err)
	}

	return &Options{viper: v}
}

func Args() []string {
	return pflag.Args()
}

func (o *Options) ReadInConfig() (*Config, error) {
	confFile := o.viper.GetString("config")

	if confFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return o.DefaultConfig(), err
		}

		confFile = filepath.Join(home, ".gau.toml")
	}

	return o.ReadConfigFile(confFile)
}

func (o *Options) ReadConfigFile(name string) (*Config, error) {
	if _, err := os.Stat(name); errors.Is(err, os.ErrNotExist) {
		return o.DefaultConfig(), fmt.Errorf("Config file %s not found, using default config", name)
	}

	o.viper.SetConfigFile(name)

	if err := o.viper.ReadInConfig(); err != nil {
		return o.DefaultConfig(), err
	}

	var c Config

	if err := o.viper.Unmarshal(&c); err != nil {
		return o.DefaultConfig(), err
	}

	o.getFlagValues(&c)

	return &c, nil
}

func (o *Options) DefaultConfig() *Config {
	c := &Config{
		Filters:           providers.Filters{},
		Proxy:             "",
		Timeout:           45,
		Threads:           1,
		Verbose:           false,
		MaxRetries:        5,
		IncludeSubdomains: false,
		RemoveParameters:  false,
		Providers:         []string{"wayback", "commoncrawl", "otx", "urlscan"},
		Blacklist:         []string{},
		JSON:              false,
		WithSources:       false,
		ProvState:         "",
		ProvFailLimit:     3,
		Outfile:           "",
	}

	o.getFlagValues(c)

	return c
}

func (o *Options) getFlagValues(c *Config) {
	version := o.viper.GetBool("version")
	verbose := o.viper.GetBool("verbose")
	json := o.viper.GetBool("json")
	// Flags use kebab-case while TOML keys use the mapstructure spellings
	// (withsources/provstate/provfaillimit); viper does not treat the two as
	// the same key. An explicitly passed flag wins; otherwise the config-file
	// value is used only when that key is actually present, so flag defaults
	// are preserved when it is absent.
	settings := o.viper.AllSettings()
	withSources := o.viper.GetBool("with-sources")
	if !pflag.CommandLine.Changed("with-sources") {
		if v, ok := settings["withsources"].(bool); ok {
			withSources = v
		}
	}
	provState := o.viper.GetString("prov-state")
	if !pflag.CommandLine.Changed("prov-state") {
		if v, ok := settings["provstate"].(string); ok {
			provState = v
		}
	}
	provFailLimit := o.viper.GetUint("prov-fail-limit")
	if !pflag.CommandLine.Changed("prov-fail-limit") {
		if _, ok := settings["provfaillimit"]; ok {
			provFailLimit = o.viper.GetUint("provfaillimit")
		}
	}
	retries := o.viper.GetUint("retries")
	proxy := o.viper.GetString("proxy")
	outfile := o.viper.GetString("o")
	fetchers := o.viper.GetStringSlice("providers")
	threads := o.viper.GetUint("threads")
	blacklist := o.viper.GetStringSlice("blacklist")
	subs := o.viper.GetBool("subs")
	fp := o.viper.GetBool("fp")

	if version {
		fmt.Printf("gau version: %s\n", providers.Version)
		os.Exit(0)
	}

	if proxy != "" {
		c.Proxy = proxy
	}

	if outfile != "" {
		c.Outfile = outfile
	}
	// set if --threads flag is set, otherwise use default
	if threads > 1 {
		c.Threads = threads
	}

	// set if --blacklist flag is specified, otherwise use default
	if len(blacklist) > 0 {
		c.Blacklist = blacklist
	}

	// set if --providers flag is specified, otherwise use default
	if len(fetchers) > 0 {
		c.Providers = fetchers
	}

	if retries > 0 {
		c.MaxRetries = retries
	}

	if subs {
		c.IncludeSubdomains = subs
	}

	if fp {
		c.RemoveParameters = fp
	}

	c.JSON = json
	c.Verbose = verbose
	c.WithSources = withSources
	c.ProvFailLimit = provFailLimit
	c.ProvState = provState
	if withSources && provState == "" {
		if home, err := os.UserHomeDir(); err == nil {
			c.ProvState = filepath.Join(home, ".gau-prov-state.json")
		} else {
			c.ProvState = "none"
		}
	}

	// get filter flags
	mc := o.viper.GetStringSlice("mc")
	fc := o.viper.GetStringSlice("fc")
	mt := o.viper.GetStringSlice("mt")
	ft := o.viper.GetStringSlice("ft")
	from := o.viper.GetString("from")
	to := o.viper.GetString("to")

	var seenFilterFlag bool

	var filters providers.Filters
	if len(mc) > 0 {
		seenFilterFlag = true
		filters.MatchStatusCodes = mc
	}

	if len(fc) > 0 {
		seenFilterFlag = true
		filters.FilterStatusCodes = fc
	}

	if len(mt) > 0 {
		seenFilterFlag = true
		filters.MatchMimeTypes = mt
	}

	if len(ft) > 0 {
		seenFilterFlag = true
		filters.FilterMimeTypes = ft
	}

	if from != "" {
		seenFilterFlag = true
		if _, err := time.Parse("200601", from); err == nil {
			filters.From = from
		}
	}

	if to != "" {
		seenFilterFlag = true
		if _, err := time.Parse("200601", to); err == nil {
			filters.To = to
		}
	}

	if seenFilterFlag {
		c.Filters = filters
	}
}
