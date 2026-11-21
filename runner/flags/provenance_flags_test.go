package flags

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lynxsecurity/pflag"
)

// withRestoredArgs swaps os.Args and the global pflag CommandLine for the
// duration of a test, so repeated New() calls don't re-register flags.
func withRestoredArgs(t *testing.T, args []string, fn func(*Options)) {
	t.Helper()
	oldArgs := os.Args
	oldCmd := pflag.CommandLine
	os.Args = append([]string{"gau"}, args...)
	pflag.CommandLine = pflag.NewFlagSet("gau", pflag.ContinueOnError)
	defer func() {
		os.Args = oldArgs
		pflag.CommandLine = oldCmd
	}()
	fn(New())
}

func TestProvenanceFlagsFromCLI(t *testing.T) {
	withRestoredArgs(t, []string{"--with-sources", "--prov-state", "/tmp/cli.json", "--prov-fail-limit", "7", "example.com"}, func(o *Options) {
		cfg := o.DefaultConfig()
		if !cfg.WithSources {
			t.Error("WithSources should be enabled by --with-sources")
		}
		if cfg.ProvState != "/tmp/cli.json" {
			t.Errorf("ProvState = %q, want /tmp/cli.json", cfg.ProvState)
		}
		if cfg.ProvFailLimit != 7 {
			t.Errorf("ProvFailLimit = %d, want 7", cfg.ProvFailLimit)
		}
	})
}

func TestProvenanceKeysFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".gau.toml")
	content := []byte(`
threads = 1
retries = 0
providers = ["otx"]
blacklist = []
json = false
withsources = true
provstate = "/tmp/toml.json"
provfaillimit = 5
`)
	if err := os.WriteFile(cfgPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	withRestoredArgs(t, []string{"--config", cfgPath, "example.com"}, func(o *Options) {
		cfg, err := o.ReadConfigFile(cfgPath)
		if err != nil {
			t.Fatalf("ReadConfigFile: %v", err)
		}
		if !cfg.WithSources {
			t.Error("withsources=true in TOML should enable provenance mode")
		}
		if cfg.ProvState != "/tmp/toml.json" {
			t.Errorf("ProvState = %q, want /tmp/toml.json", cfg.ProvState)
		}
		if cfg.ProvFailLimit != 5 {
			t.Errorf("ProvFailLimit = %d, want 5", cfg.ProvFailLimit)
		}
	})
}

func TestCLIFlagOverridesConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".gau.toml")
	if err := os.WriteFile(cfgPath, []byte("withsources = true\nprovfaillimit = 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withRestoredArgs(t, []string{"--config", cfgPath, "--prov-fail-limit", "2", "example.com"}, func(o *Options) {
		cfg, err := o.ReadConfigFile(cfgPath)
		if err != nil {
			t.Fatalf("ReadConfigFile: %v", err)
		}
		if !cfg.WithSources {
			t.Error("config-file withsources should still apply")
		}
		if cfg.ProvFailLimit != 2 {
			t.Errorf("CLI flag must win over TOML, got limit %d", cfg.ProvFailLimit)
		}
	})
}

func TestProvenanceDefaultsUnchanged(t *testing.T) {
	withRestoredArgs(t, []string{"example.com"}, func(o *Options) {
		cfg := o.DefaultConfig()
		if cfg.WithSources {
			t.Error("provenance mode must default to off")
		}
		if cfg.ProvFailLimit != 3 {
			t.Errorf("default fail limit = %d, want 3", cfg.ProvFailLimit)
		}
	})
}
