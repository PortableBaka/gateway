package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestLoadConfig_RouteTimeoutDefault(t *testing.T) {
	path := writeConfig(t, `
routes:
  - path_prefix: "/users"
    upstreams:
      - url: "http://localhost:9001"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Routes[0].Timeout; got != DefaultRouteTimeout {
		t.Errorf("Timeout = %v, want default %v", got, DefaultRouteTimeout)
	}
}

func TestLoadConfig_RouteTimeoutExplicit(t *testing.T) {
	path := writeConfig(t, `
routes:
  - path_prefix: "/users"
    timeout: 3s
    upstreams:
      - url: "http://localhost:9001"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if want := 3 * time.Second; cfg.Routes[0].Timeout != want {
		t.Errorf("Timeout = %v, want %v", cfg.Routes[0].Timeout, want)
	}
}

// TestLoadConfig_RouteTimeoutZeroFallsBackToDefault guards against a config
// that explicitly writes "timeout: 0s" — a zero value is indistinguishable
// from "not set" for time.Duration, and a zero timeout would mean every
// request to that route fails instantly, so it must fall back to the default
// rather than being accepted literally.
func TestLoadConfig_RouteTimeoutZeroFallsBackToDefault(t *testing.T) {
	path := writeConfig(t, `
routes:
  - path_prefix: "/users"
    timeout: 0s
    upstreams:
      - url: "http://localhost:9001"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Routes[0].Timeout; got != DefaultRouteTimeout {
		t.Errorf("Timeout = %v, want default %v", got, DefaultRouteTimeout)
	}
}
