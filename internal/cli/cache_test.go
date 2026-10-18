package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	cachepkg "github.com/rest-sh/restish/v2/internal/cache"
)

func newCacheApp(t *testing.T, cacheDir string) *testApp {
	t.Helper()
	app := newTestApp(t)
	app.CLI.Hooks().CachePath = cacheDir
	return app
}

func writeCacheConfig(t *testing.T, cfg map[string]any) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return writeAPIConfig(t, string(data))
}

// newCacheableServer starts a test server that counts how many times it is
// called and responds with a cacheable JSON body.
func newCacheableServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCacheSecondRequestServedFromCache verifies that the first GET hits the
// server but the second is served from cache (server call count stays at 1).
func TestCacheSecondRequestServedFromCache(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()

	first := newCacheApp(t, cacheDir)
	first.Run("get", srv.URL)
	requireContains(t, first.Stdout.String(), "ok")

	second := newCacheApp(t, cacheDir)
	second.Run("get", srv.URL)
	requireContains(t, second.Stdout.String(), "ok")

	if n := hits.Load(); n != 1 {
		t.Errorf("expected server to be called once, got %d calls", n)
	}
}

func TestCachePromptedBodyBypassesCache(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()

	for _, secret := range []string{"first-secret", "second-secret"} {
		app := newCacheApp(t, cacheDir)
		app.CLI.Hooks().PassReader = strings.NewReader(secret + "\n")
		app.Run("get", "--rsh-prompt-secret", "token", srv.URL)
	}

	if n := hits.Load(); n != 2 {
		t.Fatalf("prompted request bodies should bypass the cache, got %d server hits", n)
	}
}

func TestCacheAuthenticatedProfileRequestUsesProfileNamespace(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()
	cfgFile := writeCacheConfig(t, map[string]any{
		"apis": map[string]any{
			"svc": map[string]any{
				"base_url": srv.URL,
				"profiles": map[string]any{
					"default": map[string]any{
						"headers": []string{"Authorization: Bearer secret"},
					},
				},
			},
		},
	})

	for range 2 {
		app := newCacheApp(t, cacheDir)
		app.SetConfigPath(cfgFile)
		app.Run("get", "svc/items")
	}

	if n := hits.Load(); n != 1 {
		t.Fatalf("authenticated profile responses should share profile cache, got %d server hits", n)
	}
}

func TestCacheExplicitAuthorizationHeaderBypassesAPINamespaceCache(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		if r.Header.Get("Authorization") != "" {
			_, _ = w.Write([]byte(`{"mode":"auth"}`))
			return
		}
		_, _ = w.Write([]byte(`{"mode":"anonymous"}`))
	}))
	t.Cleanup(srv.Close)

	cacheDir := t.TempDir()
	cfgFile := writeCacheConfig(t, map[string]any{
		"apis": map[string]any{
			"svc": map[string]any{"base_url": srv.URL},
		},
	})

	anonymous := newCacheApp(t, cacheDir)
	anonymous.SetConfigPath(cfgFile)
	anonymous.Run("get", "svc/items")
	requireContains(t, anonymous.Stdout.String(), "anonymous")

	authorized := newCacheApp(t, cacheDir)
	authorized.SetConfigPath(cfgFile)
	authorized.Run("get", "-H", "Authorization: Bearer secret", "svc/items")
	requireContains(t, authorized.Stdout.String(), "auth")

	if n := hits.Load(); n != 2 {
		t.Fatalf("authorized request should bypass anonymous API cache entry, got %d server hits", n)
	}
}

func TestCacheCredentialQueryBypassesCache(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()

	for range 2 {
		newCacheApp(t, cacheDir).Run("get", srv.URL+"?api_key=secret&view=summary")
	}

	if n := hits.Load(); n != 2 {
		t.Fatalf("credential query responses should bypass cache, got %d server hits", n)
	}
}

func TestCacheProfileCredentialQueryUsesProfileNamespace(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()
	cfgFile := writeCacheConfig(t, map[string]any{
		"apis": map[string]any{
			"svc": map[string]any{
				"base_url": srv.URL,
				"profiles": map[string]any{
					"default": map[string]any{
						"query": []string{"token=secret", "view=summary"},
					},
				},
			},
		},
	})

	for range 2 {
		app := newCacheApp(t, cacheDir)
		app.SetConfigPath(cfgFile)
		app.Run("get", "svc/items")
	}

	if n := hits.Load(); n != 1 {
		t.Fatalf("profile credential query responses should share profile cache, got %d server hits", n)
	}
}

// TestCacheNoCacheBypassesCache verifies that --rsh-no-cache always hits the
// server regardless of a primed cache.
func TestCacheNoCacheBypassesCache(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()

	for range 3 {
		newCacheApp(t, cacheDir).Run("get", "--rsh-no-cache", srv.URL)
	}

	if n := hits.Load(); n != 3 {
		t.Errorf("--rsh-no-cache: expected 3 server hits, got %d", n)
	}
}

// TestCacheClearEmptiesCache verifies that "restish cache clear" removes all
// cached responses, causing the next request to hit the server again.
func TestCacheClearEmptiesCache(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()

	// Prime the cache.
	newCacheApp(t, cacheDir).Run("get", srv.URL)

	// Clear the cache.
	clear := newCacheApp(t, cacheDir)
	clear.Run("cache", "clear")
	requireContains(t, clear.Stdout.String(), "cleared")

	// Next request should hit the server again (cache is empty).
	newCacheApp(t, cacheDir).Run("get", srv.URL)

	if n := hits.Load(); n != 2 {
		t.Errorf("expected 2 server hits (prime + post-clear), got %d", n)
	}
}

func TestCacheClearPreservesSpecCache(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()
	specDir := filepath.Join(cacheDir, "specs")
	if err := os.MkdirAll(specDir, 0o700); err != nil {
		t.Fatalf("create spec cache dir: %v", err)
	}
	specPath := filepath.Join(specDir, "example.cbor")
	if err := os.WriteFile(specPath, []byte("spec metadata"), 0o600); err != nil {
		t.Fatalf("write spec cache: %v", err)
	}

	newCacheApp(t, cacheDir).Run("get", srv.URL)

	newCacheApp(t, cacheDir).Run("cache", "clear")
	if _, err := os.Stat(specPath); err != nil {
		t.Fatalf("cache clear should preserve spec metadata: %v", err)
	}

	newCacheApp(t, cacheDir).Run("get", srv.URL)
	if got := hits.Load(); got != 2 {
		t.Fatalf("expected response cache to be cleared, got %d server hits", got)
	}
}

func TestCacheClearAPIDoesNotDeleteOtherAPIOnSameHost(t *testing.T) {
	hits := map[string]*atomic.Int32{
		"/one": {},
		"/two": {},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if counter := hits[r.URL.Path]; counter != nil {
			counter.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	cacheDir := t.TempDir()
	cfgFile := writeCacheConfig(t, map[string]any{
		"apis": map[string]any{
			"one": map[string]any{"base_url": srv.URL + "/one"},
			"two": map[string]any{"base_url": srv.URL + "/two"},
		},
	})

	run := func(args ...string) {
		t.Helper()
		app := newCacheApp(t, cacheDir)
		app.SetConfigPath(cfgFile)
		app.Run(args...)
	}
	run("get", "one")
	run("get", "two")
	run("cache", "clear", "one")
	run("get", "one")
	run("get", "two")

	if got := hits["/one"].Load(); got != 2 {
		t.Fatalf("/one hits = %d, want 2", got)
	}
	if got := hits["/two"].Load(); got != 1 {
		t.Fatalf("/two hits = %d, want 1 (still cached)", got)
	}
}

func TestCacheClearDirectOnlyClearsDirectURLRequests(t *testing.T) {
	cacheDir := t.TempDir()
	directCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "")
	if err != nil {
		t.Fatalf("New direct cache: %v", err)
	}
	apiCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "demo:default")
	if err != nil {
		t.Fatalf("New API cache: %v", err)
	}
	directKey := "https://api.example.com/direct"
	apiKey := "https://api.example.com/api"
	directCache.Set(directKey, []byte("direct"))
	apiCache.Set(apiKey, []byte("api"))

	clear := newCacheApp(t, cacheDir)
	clear.Run("cache", "clear", "--direct")
	requireContains(t, clear.Stdout.String(), "Cache cleared for direct URL requests.")

	if _, ok := directCache.Get(directKey); ok {
		t.Fatal("expected direct URL request cache entry to be cleared")
	}
	if got, ok := apiCache.Get(apiKey); !ok || string(got) != "api" {
		t.Fatalf("expected API namespace cache entry to remain, got %q hit=%v", got, ok)
	}
}

func TestCacheClearAllIsAPINameNotAlias(t *testing.T) {
	app := newCacheApp(t, t.TempDir())
	app.FreshConfigPath()
	err := app.RunErr("cache", "clear", "all")
	if err == nil {
		t.Fatal("expected cache clear all to require an API named all")
	}
	if !strings.Contains(err.Error(), `unknown API or cached namespace "all"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCacheNoStoreNotCached verifies that responses with Cache-Control:
// no-store are not stored in the cache.
func TestCacheNoStoreNotCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"secret":true}`))
	}))
	t.Cleanup(srv.Close)

	cacheDir := t.TempDir()
	for range 2 {
		newCacheApp(t, cacheDir).Run("get", srv.URL)
	}

	if n := hits.Load(); n != 2 {
		t.Errorf("Cache-Control: no-store: expected 2 server hits, got %d", n)
	}
}

func TestCacheSensitiveResponseHeaderBypassesCache(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Header().Set("Set-Cookie", "session=secret")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	cacheDir := t.TempDir()
	for range 2 {
		newCacheApp(t, cacheDir).Run("get", srv.URL)
	}

	if n := hits.Load(); n != 2 {
		t.Fatalf("sensitive response headers should bypass cache, got %d server hits", n)
	}
	err := filepath.WalkDir(cacheDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "session=secret") {
			t.Fatalf("cached sensitive response header in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk cache dir: %v", err)
	}
}

// TestCacheInfo verifies that "restish cache info" prints cache statistics.
func TestCacheInfo(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()

	// Prime the cache with one entry.
	newCacheApp(t, cacheDir).Run("get", srv.URL)

	info := newCacheApp(t, cacheDir)
	info.Run("cache", "info")
	out := info.Stdout.String()
	requireContains(t, out, "Directory:", "Size:", "Entries:", "Largest hosts:", "Largest APIs/profiles:")
	if strings.Index(out, "Largest APIs/profiles:") > strings.Index(out, "Largest hosts:") {
		t.Fatalf("expected API/profile table before host table:\n%s", out)
	}
}

func TestCacheInfoJSONOutput(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()

	newCacheApp(t, cacheDir).Run("get", srv.URL)

	info := newCacheApp(t, cacheDir)
	info.Run("cache", "info", "-o", "json")
	var got struct {
		Directory string `json:"directory"`
		SizeBytes int64  `json:"size_bytes"`
		Size      string `json:"size"`
		Entries   int    `json:"entries"`
		TopHosts  []struct {
			Name      string `json:"name"`
			SizeBytes int64  `json:"size_bytes"`
			Size      string `json:"size"`
			Percent   string `json:"percent"`
			Entries   int    `json:"entries"`
		} `json:"top_hosts"`
		TopAPIProfiles []struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			API       string `json:"api"`
			Profile   string `json:"profile"`
			SizeBytes int64  `json:"size_bytes"`
			Size      string `json:"size"`
			Percent   string `json:"percent"`
			Entries   int    `json:"entries"`
		} `json:"top_api_profiles"`
	}
	if err := json.Unmarshal(info.Stdout.Bytes(), &got); err != nil {
		t.Fatalf("parse JSON output: %v\n%s", err, info.Stdout.String())
	}
	if got.Directory != cacheDir || got.Entries == 0 || got.Size == "" || got.SizeBytes <= 0 {
		t.Fatalf("cache info JSON = %#v", got)
	}
	if len(got.TopHosts) == 0 || got.TopHosts[0].Name == "" || got.TopHosts[0].SizeBytes <= 0 || got.TopHosts[0].Size == "" || got.TopHosts[0].Percent == "" || got.TopHosts[0].Entries == 0 {
		t.Fatalf("cache info JSON top_hosts = %#v", got.TopHosts)
	}
	if len(got.TopAPIProfiles) == 0 || got.TopAPIProfiles[0].Name == "" || got.TopAPIProfiles[0].Namespace == "" || got.TopAPIProfiles[0].SizeBytes <= 0 || got.TopAPIProfiles[0].Size == "" || got.TopAPIProfiles[0].Percent == "" || got.TopAPIProfiles[0].Entries == 0 {
		t.Fatalf("cache info JSON top_api_profiles = %#v", got.TopAPIProfiles)
	}
}

func TestCacheInfoShowsAPIProfilePercent(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	cacheDir := t.TempDir()
	cfgFile := writeCacheConfig(t, map[string]any{
		"apis": map[string]any{
			"demo": map[string]any{"base_url": srv.URL},
		},
	})

	prime := newCacheApp(t, cacheDir)
	prime.SetConfigPath(cfgFile)
	prime.Run("get", "demo")

	info := newCacheApp(t, cacheDir)
	info.SetConfigPath(cfgFile)
	info.Run("cache", "info")
	out := info.Stdout.String()
	requireContains(t, out, "Largest hosts:", "Largest APIs/profiles:", "demo (default)", "100.0%")
	if strings.Contains(out, "clear:") {
		t.Fatalf("cache info should not show clear command hints:\n%s", out)
	}
}

func TestCacheInfoAlignsWideSizeColumn(t *testing.T) {
	cacheDir := t.TempDir()
	stripeCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "stripe:default")
	if err != nil {
		t.Fatalf("New stripe cache: %v", err)
	}
	directCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "")
	if err != nil {
		t.Fatalf("New direct cache: %v", err)
	}
	stripeCache.Set("https://stripe.example.com/items", []byte(strings.Repeat("s", 105*1024)))
	directCache.Set("https://direct.example.com/items", []byte(strings.Repeat("d", 82*1024)))

	info := newCacheApp(t, cacheDir)
	info.Run("cache", "info")
	lines := strings.Split(info.Stdout.String(), "\n")
	var stripeLine, directLine string
	for _, line := range lines {
		if strings.Contains(line, "stripe (default, unregistered)") {
			stripeLine = line
		}
		if strings.Contains(line, "(direct URL requests)") {
			directLine = line
		}
	}
	if stripeLine == "" || directLine == "" {
		t.Fatalf("missing expected cache info lines:\n%s", info.Stdout.String())
	}
	stripePercent := strings.Index(stripeLine, "%")
	directPercent := strings.Index(directLine, "%")
	if stripePercent < 0 || directPercent < 0 || stripePercent != directPercent {
		t.Fatalf("percentage columns misaligned:\n%s\n%s", stripeLine, directLine)
	}
}

func TestCacheInfoShowsAndClearsUnregisteredNamespace(t *testing.T) {
	cacheDir := t.TempDir()
	staleCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "tapi:default")
	if err != nil {
		t.Fatalf("New stale cache: %v", err)
	}
	key := "https://api.example.com/items"
	staleCache.Set(key, []byte("cached"))

	info := newCacheApp(t, cacheDir)
	info.Run("cache", "info")
	out := info.Stdout.String()
	requireContains(t, out, "tapi (default, unregistered)", "100.0%")
	if strings.Contains(out, "clear:") {
		t.Fatalf("cache info should not show clear command hints:\n%s", out)
	}

	clear := newCacheApp(t, cacheDir)
	clear.Run("cache", "clear", "tapi")
	requireContains(t, clear.Stdout.String(), `Cache cleared for cached namespace "tapi".`)
	if _, ok := staleCache.Get(key); ok {
		t.Fatal("expected stale namespace cache entry to be cleared")
	}
}

func TestCacheInfoShowsProjectAPINamespaceAsRegistered(t *testing.T) {
	var hits atomic.Int32
	srv := newCacheableServer(t, &hits)
	_, _, projectDir := setupProjectConfigTest(t)
	writeProjectConfigTestFile(t, filepath.Join(projectDir, ".restish.json"), `{
  "apis": {
    "svc": {"base_url": "`+srv.URL+`"}
  }
}`)
	t.Chdir(projectDir)

	trust, _, _ := newProjectConfigTestCLI(t)
	if err := trust.Run([]string{"restish", "config", "trust"}); err != nil {
		t.Fatalf("config trust: %v", err)
	}

	prime, _, _ := newProjectConfigTestCLI(t)
	if err := prime.Run([]string{"restish", "get", "svc"}); err != nil {
		t.Fatalf("get svc: %v", err)
	}

	info, out, _ := newProjectConfigTestCLI(t)
	if err := info.Run([]string{"restish", "cache", "info"}); err != nil {
		t.Fatalf("cache info: %v", err)
	}
	got := out.String()
	requireContains(t, got, "Largest APIs/profiles:", "svc (default)", "100.0%")
	if strings.Contains(got, "unregistered") || strings.Contains(got, "project-") {
		t.Fatalf("cache info output = %q, want project API shown as registered logical API", got)
	}

	jsonInfo, jsonOut, _ := newProjectConfigTestCLI(t)
	if err := jsonInfo.Run([]string{"restish", "cache", "info", "-o", "json"}); err != nil {
		t.Fatalf("cache info json: %v", err)
	}
	var decoded struct {
		TopAPIProfiles []struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			API       string `json:"api"`
			Profile   string `json:"profile"`
		} `json:"top_api_profiles"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatalf("parse cache info JSON: %v\n%s", err, jsonOut.String())
	}
	if len(decoded.TopAPIProfiles) == 0 {
		t.Fatalf("cache info JSON top_api_profiles empty: %s", jsonOut.String())
	}
	top := decoded.TopAPIProfiles[0]
	if top.Name != "svc (default)" || top.API != "svc" || top.Profile != "default" || !strings.HasPrefix(top.Namespace, "project-") {
		t.Fatalf("top API profile = %#v, want logical project API with project namespace", top)
	}
}

func TestCacheInfoTTYShowsTreemap(t *testing.T) {
	cacheDir := t.TempDir()
	demoCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "demo:default")
	if err != nil {
		t.Fatalf("New demo cache: %v", err)
	}
	adminCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "admin:prod")
	if err != nil {
		t.Fatalf("New admin cache: %v", err)
	}
	demoCache.Set("https://api.example.com/a", []byte(strings.Repeat("a", 80)))
	adminCache.Set("https://admin.example.com/a", []byte(strings.Repeat("b", 20)))

	cfgFile := writeCacheConfig(t, map[string]any{
		"apis": map[string]any{
			"demo":  map[string]any{"base_url": "https://api.example.com"},
			"admin": map[string]any{"base_url": "https://admin.example.com"},
		},
	})

	info := newCacheApp(t, cacheDir)
	info.SetConfigPath(cfgFile)
	info.SetStdoutTTY(true)
	info.Run("cache", "info")
	requireContains(t, info.Stdout.String(), "Usage map by API/profile:", "╭", "█", "demo (default)", "admin (prod)", "80%")
}
