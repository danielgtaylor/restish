package cli_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
)

type noopAuthHandler struct{}

func (noopAuthHandler) Parameters() []auth.Param { return nil }

func (noopAuthHandler) Authenticate(context.Context, *http.Request, auth.AuthContext) error {
	return nil
}

func TestAPIAuthInspectAddRemove(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", &config.ProfileConfig{}))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	app.Run("api", "auth", "add", "myapi", "PartnerKey")
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if written.APIs["myapi"].Profiles["default"].Credentials["PartnerKey"] == nil {
		t.Fatal("expected PartnerKey credential")
	}

	app.Stdout.Reset()
	app.Run("api", "auth", "inspect", "myapi")
	requireContains(t, app.Stdout.String(), "PartnerKey")

	app.Run("api", "auth", "remove", "myapi", "PartnerKey")
	written, err = config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if written.APIs["myapi"].Profiles["default"].Credentials != nil {
		t.Fatalf("expected credentials removed, got %#v", written.APIs["myapi"].Profiles["default"].Credentials)
	}
}

func TestAPIAuthInspectRejectsResponseTransformFlags(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileAuth(apiKeyAuth("header", "X-API-Key", "secret"))))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	err := app.RunErr("api", "auth", "inspect", "myapi", "-o", "json")
	if err == nil {
		t.Fatal("expected api auth inspect -o json to fail")
	}
	if !strings.Contains(err.Error(), "does not support -o/--rsh-output-format") {
		t.Fatalf("unexpected api auth inspect error: %v", err)
	}
}

func TestAPIAuthListCommandRemoved(t *testing.T) {
	err := newTestApp(t).RunErr("api", "auth", "list", "myapi")
	if err == nil {
		t.Fatal("expected api auth list to be removed")
	}
	if !strings.Contains(err.Error(), `unknown command "list"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIAuthHeaderCommandRemoved(t *testing.T) {
	err := newTestApp(t).RunErr("api", "auth", "header", "myapi", "Authorization")
	if err == nil {
		t.Fatal("expected api auth header to be removed")
	}
	if !strings.Contains(err.Error(), `unknown command "header"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIAuthRemoveMissingCredentialFails(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": {},
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	err := app.RunErr("api", "auth", "remove", "myapi", "Missing")
	if err == nil {
		t.Fatal("expected missing credential removal to fail")
	}
	if !strings.Contains(err.Error(), `profile "default" of API "myapi" has no credential "Missing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if written.APIs["myapi"].Profiles["default"].Credentials["PartnerKey"] == nil {
		t.Fatal("existing credential should remain")
	}
}

func TestAPIAuthConcurrentAddsPreserveCredentials(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"Existing": {},
	})))

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, credentialID := range []string{"RaceA", "RaceB"} {
		credentialID := credentialID
		wg.Add(1)
		go func() {
			defer wg.Done()
			app := newTestApp(t)
			app.SetConfigPath(cfgFile)
			errCh <- app.RunErr("api", "auth", "add", "myapi", credentialID)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("api auth add: %v", err)
		}
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	creds := written.APIs["myapi"].Profiles["default"].Credentials
	for _, credentialID := range []string{"Existing", "RaceA", "RaceB"} {
		if creds[credentialID] == nil {
			t.Fatalf("credential %q missing after concurrent adds: %#v", credentialID, creds)
		}
	}
}

func TestAPIAuthAddRemoveSuccessMessages(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  "apis": {
    "myapi": {
      "base_url": "https://api.example.com",
      "profiles": {
        "default": {
          "credentials": {
            "Existing": {}
          }
        }
      }
    }
  }
}`)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "auth", "add", "myapi", "PartnerKey"}); err != nil {
		t.Fatalf("api auth add: %v", err)
	}
	requireContains(t, out.String(),
		"Wrote config: "+cfgFile,
		`Added credential "PartnerKey" to API "myapi" profile "default".`,
		`Next: run "restish api auth inspect myapi" to review credential readiness.`,
	)

	out.Reset()
	if err := c.Run([]string{"restish", "api", "auth", "remove", "myapi", "Existing"}); err != nil {
		t.Fatalf("api auth remove: %v", err)
	}
	requireContains(t, out.String(),
		"Wrote config: "+cfgFile,
		`Removed credential "Existing" from API "myapi" profile "default".`,
	)
}

func TestAPIAuthInspectCredentialAPIKey(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("header", "X-API-Key", "secret")),
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	app.Run("api", "auth", "inspect", "myapi", "--credential", "PartnerKey")
	got := app.Stdout.String()
	if !strings.Contains(got, "X-API-Key: secret") {
		t.Fatalf("expected API key inspection to show value, got %q", got)
	}

	app.Stdout.Reset()
	app.Run("api", "auth", "inspect", "myapi", "--credential", "PartnerKey", "--redact")
	got = app.Stdout.String()
	if strings.Contains(got, "secret") || !strings.Contains(got, "X-API-Key: <redacted>") {
		t.Fatalf("expected redacted API key inspection with --redact, got %q", got)
	}
}

func TestAPIAuthInspectCredentialAPIKeyQuery(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("query", "partner", "secret")),
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	app.Run("api", "auth", "inspect", "myapi", "--credential", "PartnerKey")
	got := app.Stdout.String()
	if !strings.Contains(got, "Query: https://api.example.com?partner=secret") {
		t.Fatalf("expected API key query inspection to show value, got %q", got)
	}

	app.Stdout.Reset()
	app.Run("api", "auth", "inspect", "myapi", "--credential", "PartnerKey", "--redact")
	got = app.Stdout.String()
	if strings.Contains(got, "secret") || !strings.Contains(got, "Query: https://api.example.com?partner=%3Credacted%3E") {
		t.Fatalf("expected redacted API key query inspection with --redact, got %q", got)
	}
}

func TestAPIAuthInspectURLSuggestsV2Form(t *testing.T) {
	err := newTestApp(t).RunErr("api", "auth", "inspect", "https://api.example.com/items")
	if err == nil {
		t.Fatal("expected URL argument error")
	}
	if !strings.Contains(err.Error(), "v2 form: restish api auth get <api-name>") {
		t.Fatalf("expected v2 form hint, got: %v", err)
	}
}

func TestAPIAuthInspectSingleCredentialByDefault(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "secret")),
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	app.Run("api", "auth", "inspect", "myapi")
	got := app.Stdout.String()
	if !strings.Contains(got, "Credential: PartnerKey") {
		t.Fatalf("expected selected credential in output, got %q", got)
	}
	if !strings.Contains(got, "X-Partner-Key: secret") {
		t.Fatalf("expected API key inspection to show value, got %q", got)
	}

	app.Stdout.Reset()
	app.Run("api", "auth", "get", "myapi")
	if got := strings.TrimSpace(app.Stdout.String()); got != "X-Partner-Key: secret" {
		t.Fatalf("auth get = %q, want X-Partner-Key: secret", got)
	}

	app.Stdout.Reset()
	app.Run("api", "auth", "get", "myapi", "PartnerKey")
	if got := strings.TrimSpace(app.Stdout.String()); got != "X-Partner-Key: secret" {
		t.Fatalf("auth get with positional credential = %q, want X-Partner-Key: secret", got)
	}
}

func TestAPIAuthInspectMultipleCredentialsShowsAll(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "secret")),
		"UserBearer": testCredential(bearerAuth("user-token")),
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	app.Run("api", "auth", "inspect", "myapi")
	got := app.Stdout.String()
	requireContains(t, got,
		"Credential: PartnerKey",
		"Auth type: api-key",
		"X-Partner-Key: secret",
		"Credential: UserBearer",
		"Auth type: bearer",
		"Authorization: Bearer user-token",
	)

	app.Stdout.Reset()
	app.Run("api", "auth", "inspect", "myapi", "--redact")
	got = app.Stdout.String()
	requireContains(t, got,
		"Credential: PartnerKey",
		"X-Partner-Key: <redacted>",
		"Credential: UserBearer",
		"Authorization: <redacted>",
	)
	if strings.Contains(got, "secret") || strings.Contains(got, "user-token") {
		t.Fatalf("redacted inspect output leaked secret:\n%s", got)
	}

	app.Stdout.Reset()
	err := app.RunErr("api", "auth", "get", "myapi")
	if err == nil {
		t.Fatal("expected api auth get to require a credential when multiple are configured")
	}
	requireContains(t, err.Error(), "multiple configured credentials", "restish api auth get myapi <credential-id>")
}

func TestAPIAuthInspectUsesCachedOperationMetadata(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(
				`"UserOAuth":{"type":"oauth2","flows":{"authorizationCode":{"authorizationUrl":"https://auth.example.com/authorize","tokenUrl":"https://auth.example.com/token","scopes":{"items:read":"Read items"}}}}`,
				`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`,
				`"OldKey":{"type":"apiKey","in":"header","name":"X-Old-Key","deprecated":true}`,
				`"MutualTLS":{"type":"mutualTLS"}`,
				`"urn:example:auth:TenantKey":{"type":"apiKey","in":"header","name":"X-Tenant-Key"}`),
			openAPISecurity(`{"UserOAuth":["items:read"]}`),
			openAPIPaths(
				openAPIGet("/items", "listItems"),
				openAPIGet("/partner", "partnerReport", `"security":[{"PartnerKey":[]}]`),
				openAPIGet("/old", "oldReport", `"security":[{"OldKey":[]}]`),
				openAPIGet("/mtls", "mtlsReport", `"security":[{"MutualTLS":[]}]`),
				openAPIGet("/ghost", "ghostReport", `"security":[{"GhostAuth":[]}]`),
				openAPIGet("/tenant", "tenantReport", `"security":[{"urn:example:auth:TenantKey":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"UserOAuth": testCredential(bearerAuth("user-token"), "items:read"),
	})))
	if err := os.Chmod(env.cfgFile, 0o600); err != nil {
		t.Fatal(err)
	}

	c, out := env.newCaptureCLI()
	loader := &countingLoader{}
	c.AddLoader(loader)
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi"}); err != nil {
		t.Fatalf("api auth inspect: %v", err)
	}
	if got := loader.detects.Load(); got != 0 {
		t.Fatalf("api auth inspect loaded spec via loader %d times; want cached operation metadata only", got)
	}
	got := out.String()
	requireContains(t, got,
		"Callable secured operations: 1/6",
		"UserOAuth: configured, needs items:read, satisfies items:read",
		"PartnerKey: missing",
		"OldKey: missing, deprecated",
		"MutualTLS: missing, 1 operation",
		"GhostAuth: missing, unsupported unknown, undeclared security scheme",
		"urn:example:auth:TenantKey: missing, URI-backed",
	)
}

func TestAPIAuthInspectDefersDynamicDPoPMaterialWithoutCallingTheSource(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "DPoP API",
			openAPISecuritySchemes(`"RealmrootOAuth":{"type":"oauth2","x-dpop-required":true,"flows":{"clientCredentials":{"tokenUrl":"https://identity.example/token","scopes":{"wallet:read":"Read Wallet"}}}}`),
			openAPIPaths(openAPIGet("/wallet", "readWallet", `"security":[{"RealmrootOAuth":["wallet:read"]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"RealmrootOAuth": testCredential(&config.AuthConfig{Type: "dpop", Params: map[string]string{
			"source": "missing-plugin", "reference": "https://identity.example/resources/wallet",
		}}),
	})))

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi", "--operation", "readWallet", "--redact"}); err != nil {
		t.Fatalf("api auth inspect: %v", err)
	}
	requireContains(t, out.String(),
		"Operation: readWallet",
		"Auth type: dpop",
		"Credential source: missing-plugin",
		"Operation scopes: wallet:read",
		"Auth material: evaluated when the operation is invoked",
	)
}

func TestAPIAuthInspectReportsEnvReadinessAndProfileFallback(t *testing.T) {
	t.Setenv("READY_TOKEN", "ready")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(
				`"BearerAuth":{"type":"http","scheme":"bearer"}`,
				`"InferenceBearer":{"type":"http","scheme":"bearer"}`,
				`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
			openAPIPaths(
				openAPIGet("/me", "me", `"security":[{"BearerAuth":[]}]`),
				openAPIGet("/models", "models", `"security":[{"InferenceBearer":[]}]`),
				openAPIGet("/partner", "partner", `"security":[{"PartnerKey":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), &config.ProfileConfig{
		Auth: bearerAuth("env:READY_TOKEN"),
		Credentials: map[string]*config.CredentialConfig{
			"BearerAuth": testCredential(bearerAuth("env:MISSING_TOKEN")),
		},
	}))

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi"}); err != nil {
		t.Fatalf("api auth inspect: %v", err)
	}
	got := out.String()
	requireContains(t, got,
		"Generic request auth: configured",
		"Callable secured operations: 3/3",
		"BearerAuth: configured (env missing: MISSING_TOKEN)",
		"InferenceBearer: missing, satisfied by profile auth fallback",
		"PartnerKey: missing, satisfied by profile auth fallback (unchecked auth kind)",
	)
}

func TestAPIAuthInspectCountsOptionalAnonymousSecurityAsCallable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Optional Auth API",
			openAPISecuritySchemes(`"ApiKey":{"type":"apiKey","in":"header","name":"X-API-Key"}`),
			openAPISecurity(`{}`, `{"ApiKey":[]}`),
			openAPIPaths(
				openAPIGet("/optional", "optional"),
				openAPIGet("/required", "required", `"security":[{"ApiKey":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"ApiKey": testCredential(apiKeyAuth("header", "X-API-Key", "env:MISSING_API_KEY")),
	})))

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi"}); err != nil {
		t.Fatalf("api auth inspect: %v", err)
	}
	got := out.String()
	requireContains(t, got,
		"Callable secured operations: 1/2",
		"ApiKey: configured (env missing: MISSING_API_KEY), 2 operations",
	)
}

func TestAPIAuthInspectOAuthAuthorizationCodeRequiresCachedTokenAndExactScopes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "OAuth API", "version": "1.0"},
  "servers": [{"url": %q}],
  "components": {
    "securitySchemes": {
      "OAuth": {
        "type": "oauth2",
        "flows": {
          "authorizationCode": {
            "authorizationUrl": "https://auth.example.com/authorize",
            "tokenUrl": "https://auth.example.com/token",
            "scopes": {
              "read:profile": "Read profile",
              "read:recovery": "Read recovery"
            }
          }
        }
      }
    }
  },
  "paths": {
    "/profile": {"get": {"operationId": "profile", "security": [{"OAuth": ["read:profile"]}], "responses": {"200": {"description": "OK"}}}},
    "/recovery": {"get": {"operationId": "recovery", "security": [{"OAuth": ["read:recovery"]}], "responses": {"200": {"description": "OK"}}}}
  }
}`, baseURL)
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"OAuth": testCredential(&config.AuthConfig{Type: "oauth-authorization-code", Params: map[string]string{
			"client_id":     "client",
			"authorize_url": "https://auth.example.com/authorize",
			"token_url":     "https://auth.example.com/token",
			"scopes":        "read:profile",
		}}, "read:profile"),
	})))
	tokenPath := filepath.Join(t.TempDir(), "tokens.cbor")

	c, out := env.newCaptureCLI()
	c.Hooks().TokenCachePath = tokenPath
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi"}); err != nil {
		t.Fatalf("api auth inspect: %v", err)
	}
	got := out.String()
	requireContains(t, got,
		"Callable secured operations: 0/2",
		"OAuth: configured (OAuth access token not cached), needs read:profile read:recovery, satisfies read:profile",
	)

	// The cache key includes token-shaping OAuth params so only compatible
	// auth-code tokens are shared across APIs.
	oauthCacheKey := oauthAuthCodeCacheKeyForTest(map[string]string{
		"client_id":     "client",
		"authorize_url": "https://auth.example.com/authorize",
		"token_url":     "https://auth.example.com/token",
		"scopes":        "read:profile",
	})
	if err := auth.NewTokenCache(tokenPath).Set(oauthCacheKey, auth.CachedToken{
		AccessToken: "cached-token",
		Expiry:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("write token cache: %v", err)
	}
	c, out = env.newCaptureCLI()
	c.Hooks().TokenCachePath = tokenPath
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi"}); err != nil {
		t.Fatalf("api auth inspect with token: %v", err)
	}
	got = out.String()
	requireContains(t, got,
		"Callable secured operations: 1/2",
		"OAuth: configured, needs read:profile read:recovery, satisfies read:profile",
	)
}

func TestAPIAuthInspectOperationLabelsProfileFallbackSource(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(`"InferenceBearer":{"type":"http","scheme":"bearer"}`),
			openAPIPaths(openAPIGet("/models", "listModels", `"security":[{"InferenceBearer":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileAuth(basicAuth("u", "p"))))

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi", "--operation", "list-models"}); err != nil {
		t.Fatalf("api auth inspect operation: %v", err)
	}
	got := out.String()
	requireContains(t, got,
		"Operation: listModels",
		"Credentials: InferenceBearer",
		"Source: profile auth fallback",
		"Auth type: http-basic",
		"Authorization: Basic dTpw",
	)
}

func TestAPIAuthInspectFallbackOperationNameUsesOperationBase(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
			openAPIPaths(openAPIGet("/api/rest/widgets", "", `"security":[{"PartnerKey":[]}]`)))
	})
	env.writeAPIConfig(t, &config.APIConfig{
		BaseURL:       env.baseURL(t),
		OperationBase: "/api/rest",
		Profiles: map[string]*config.ProfileConfig{
			"default": profileAuth(apiKeyAuth("header", "X-Partner-Key", "sekret")),
		},
	})

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi", "--operation", "get-widgets"}); err != nil {
		t.Fatalf("api auth inspect operation: %v", err)
	}
	requireContains(t, out.String(),
		"X-Partner-Key: sekret",
	)
}

func TestAPIAuthInspectUsesImplicitDefaultProfile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
			openAPISecurity(`{"PartnerKey":[]}`),
			openAPIPaths(openAPIGet("/partner", "partnerReport")))
	})
	env.writeAPIConfig(t, &config.APIConfig{BaseURL: env.baseURL(t)})

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi"}); err != nil {
		t.Fatalf("api auth inspect: %v", err)
	}
	got := out.String()
	requireContains(t, got,
		"Profile: default",
		"Generic request auth: none",
		"Credentials: none",
		"PartnerKey: missing",
		`Next: run "restish api auth add tapi PartnerKey".`,
	)

	c, _ = env.newCaptureCLI()
	err := c.Run([]string{"restish", "api", "auth", "get", "tapi"})
	if err == nil {
		t.Fatal("expected api auth get with no auth to fail")
	}
	requireContains(t, err.Error(), "has no auth config", "restish api auth inspect tapi", "restish api auth add tapi <credential-id>")
}

func TestAPIAuthAddDerivesAuthAndPromptsFromCachedSpec(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
			openAPIPaths(openAPIGet("/partner", "partnerReport", `"security":[{"PartnerKey":[]}]`)))
	})
	env.writeAPIConfig(t, &config.APIConfig{
		BaseURL: env.baseURL(t),
		Profiles: map[string]*config.ProfileConfig{
			"default": {},
		},
	})
	if err := os.Chmod(env.cfgFile, 0o600); err != nil {
		t.Fatal(err)
	}

	c := env.newCLI()
	c.Hooks().PassReader = strings.NewReader("partner-secret\n")
	if err := c.Run([]string{"restish", "api", "auth", "add", "tapi", "PartnerKey"}); err != nil {
		t.Fatalf("api auth add: %v", err)
	}
	written, err := config.Load(env.cfgFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	credential := written.APIs["tapi"].Profiles["default"].Credentials["PartnerKey"]
	if credential == nil || credential.Auth == nil {
		t.Fatalf("credential = %#v", credential)
	}
	if credential.Auth.Type != "api-key" || credential.Auth.Params["name"] != "X-Partner-Key" || credential.Auth.Params["value"] != "partner-secret" {
		t.Fatalf("auth = %#v", credential.Auth)
	}
}

func TestAPIAuthInspectOperationFallsBackToRawSpecCache(t *testing.T) {
	dir := t.TempDir()
	specPath := dir + "/openapi.yaml"
	if err := os.WriteFile(specPath, []byte(`openapi: 3.0.3
info:
  title: Local Auth API
  version: "1.0"
servers:
  - url: http://127.0.0.1:8898
security:
  - bearer: []
components:
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
paths:
  /private:
    get:
      operationId: privateEcho
      responses:
        "200": {description: OK}
  /public:
    get:
      operationId: publicEcho
      security: []
      responses:
        "200": {description: OK}
`), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	cfgFile := dir + "/restish.json"
	if err := os.WriteFile(cfgFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cacheDir := dir + "/spec-cache"

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	if err := c.Run([]string{"restish", "api", "connect", "controlauth", "http://127.0.0.1:8898", "--spec", specPath, "--replace", "prompt.credentials.bearer.token: local-secret"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	if err := c.Run([]string{"restish", "api", "auth", "get", "controlauth", "--operation", "privateEcho"}); err != nil {
		t.Fatalf("api auth get private operation: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "Authorization: Bearer local-secret" {
		t.Fatalf("auth get = %q, want Authorization: Bearer local-secret", got)
	}

	out.Reset()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "controlauth", "--operation", "public-echo"}); err != nil {
		t.Fatalf("api auth inspect public operation: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Auth: none") {
		t.Fatalf("expected no-auth public operation, got:\n%s", got)
	}
}

func TestAPIAuthInspectAnonymousOnlyOperationSuppressesProfileAuth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Anonymous API",
			openAPISecuritySchemes(`"ApiKeyAuth":{"type":"apiKey","in":"header","name":"X-API-Key"}`),
			openAPISecurity(`{}`),
			openAPIPaths(
				openAPIGet("/public", "publicReport"),
				openAPIGet("/private", "privateReport", `"security":[{"ApiKeyAuth":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), &config.ProfileConfig{
		Auth: apiKeyAuth("header", "X-Profile-Key", "env:MISSING_PROFILE_KEY"),
		Credentials: map[string]*config.CredentialConfig{
			"ApiKeyAuth": testCredential(apiKeyAuth("header", "X-API-Key", "env:MISSING_PROFILE_KEY")),
		},
	}))

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi", "--operation", "public-report"}); err != nil {
		t.Fatalf("api auth inspect anonymous operation: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Auth: none (security: [{}])") {
		t.Fatalf("expected anonymous-only operation auth, got:\n%s", got)
	}

	out.Reset()
	err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi", "--operation", "private-report"})
	if err == nil || !strings.Contains(err.Error(), "MISSING_PROFILE_KEY") {
		t.Fatalf("private operation error = %v, want missing env credential", err)
	}
}

func TestAPIAuthInspectOperationCombinedCredentials(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(
				`"UserKey":{"type":"apiKey","in":"header","name":"X-User-Key"}`,
				`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
			openAPIPaths(openAPIGet("/signed", "signedReport", `"security":[{"UserKey":[],"PartnerKey":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"UserKey":    testCredential(apiKeyAuth("header", "X-User-Key", "user-secret")),
		"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "partner-secret")),
	})))
	if err := os.Chmod(env.cfgFile, 0o600); err != nil {
		t.Fatal(err)
	}

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi", "--operation", "signed-report"}); err != nil {
		t.Fatalf("api auth inspect operation: %v", err)
	}
	got := out.String()
	requireContains(t, got,
		"Operation: signedReport",
		"Credentials: UserKey, PartnerKey",
		"X-User-Key: user-secret",
		"X-Partner-Key: partner-secret",
	)

	out.Reset()
	if err := c.Run([]string{"restish", "api", "auth", "inspect", "tapi", "--operation", "signedReport", "--redact"}); err != nil {
		t.Fatalf("api auth inspect redacted operation: %v", err)
	}
	got = out.String()
	requireContains(t, got,
		"X-User-Key: <redacted>",
		"X-Partner-Key: <redacted>",
	)
	if strings.Contains(got, "user-secret") || strings.Contains(got, "partner-secret") {
		t.Fatalf("redacted inspect output leaked secret:\n%s", got)
	}

	out.Reset()
	err := c.Run([]string{"restish", "api", "auth", "get", "tapi", "--operation", "signedReport"})
	if err == nil {
		t.Fatal("expected combined operation auth get to require a single fragment")
	}
	requireContains(t, err.Error(), "auth produced multiple header/query fragments", "api auth inspect tapi --operation signedReport")
}

func TestAPIAuthGetCredentialAPIKeyQuery(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("query", "partner", "secret")),
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	app.Run("api", "auth", "get", "myapi", "PartnerKey")
	if got := strings.TrimSpace(app.Stdout.String()); got != "?partner=secret" {
		t.Fatalf("auth get query = %q, want ?partner=secret", got)
	}
}

func TestAPIAuthGetPrintHeaderHeader(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("header", "X-API-Key", "secret")),
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	app.Run("api", "auth", "get", "myapi", "PartnerKey", "--print-header")
	if got := strings.TrimSpace(app.Stdout.String()); got != "X-API-Key: secret" {
		t.Fatalf("auth get --print-header = %q, want %q", got, "X-API-Key: secret")
	}
}

func TestAPIAuthGetPrintHeaderQueryFails(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("query", "partner", "secret")),
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	err := app.RunErr("api", "auth", "get", "myapi", "PartnerKey", "--print-header")
	if err == nil {
		t.Fatal("expected --print-header on query auth to fail")
	}
	if !strings.Contains(err.Error(), "query parameter") {
		t.Errorf("error = %q, want it to mention query parameter", err)
	}
}

func TestAPIAuthGetPrintHeaderCookieFails(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", testAPIConfig("https://api.example.com", profileCredentials(map[string]*config.CredentialConfig{
		"Session": testCredential(apiKeyAuth("cookie", "session", "secret")),
	})))

	app := newTestApp(t)
	app.SetConfigPath(cfgFile)
	err := app.RunErr("api", "auth", "get", "myapi", "Session", "--print-header")
	if err == nil {
		t.Fatal("expected --print-header on cookie auth to fail")
	}
	if !strings.Contains(err.Error(), "cookie") {
		t.Errorf("error = %q, want it to mention cookie", err)
	}
}

func TestAPIAuthGetPrintHeaderOperationHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
			openAPIPaths(openAPIGet("/partner", "partnerReport", `"security":[{"PartnerKey":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "sekret")),
	})))

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "api", "auth", "get", "tapi", "--operation", "partner-report", "--print-header"}); err != nil {
		t.Fatalf("api auth get operation --print-header: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "X-Partner-Key: sekret" {
		t.Fatalf("auth get operation --print-header = %q, want X-Partner-Key: sekret", got)
	}
}

func TestAPIAuthGetPrintHeaderOperationQueryFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(`"PartnerKey":{"type":"apiKey","in":"query","name":"partner"}`),
			openAPIPaths(openAPIGet("/partner", "partnerReport", `"security":[{"PartnerKey":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"PartnerKey": testCredential(apiKeyAuth("query", "partner", "sekret")),
	})))

	c, _ := env.newCaptureCLI()
	err := c.Run([]string{"restish", "api", "auth", "get", "tapi", "--operation", "partner-report", "--print-header"})
	if err == nil {
		t.Fatal("expected operation --print-header on query auth to fail")
	}
	if !strings.Contains(err.Error(), "query parameter") {
		t.Errorf("error = %q, want it to mention query parameter", err)
	}
}

func TestAPIAuthGetPrintHeaderOperationCookieFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(`"Session":{"type":"apiKey","in":"cookie","name":"session"}`),
			openAPIPaths(openAPIGet("/session", "sessionReport", `"security":[{"Session":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"Session": testCredential(apiKeyAuth("cookie", "session", "sekret")),
	})))

	c, _ := env.newCaptureCLI()
	err := c.Run([]string{"restish", "api", "auth", "get", "tapi", "--operation", "session-report", "--print-header"})
	if err == nil {
		t.Fatal("expected operation --print-header on cookie auth to fail")
	}
	if !strings.Contains(err.Error(), "cookie") {
		t.Errorf("error = %q, want it to mention cookie", err)
	}
}

func TestAPIAuthGetPrintHeaderOperationNoHeaderFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(`"NoHeader":{"type":"apiKey","in":"header","name":"X-No-Header"}`),
			openAPIPaths(openAPIGet("/silent", "silentReport", `"security":[{"NoHeader":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"NoHeader": testCredential(&config.AuthConfig{Type: "noop-auth"}),
	})))

	c, _ := env.newCaptureCLI()
	c.AddAuthHandler("noop-auth", noopAuthHandler{})
	err := c.Run([]string{"restish", "api", "auth", "get", "tapi", "--operation", "silent-report", "--print-header"})
	if err == nil {
		t.Fatal("expected operation --print-header with no header to fail")
	}
	if !strings.Contains(err.Error(), "did not produce a header value") {
		t.Errorf("error = %q, want it to mention no header value", err)
	}
}

func TestAPIAuthGetPrintHeaderOperationMultipleHeadersFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return openAPISpec(baseURL, "Auth API",
			openAPISecuritySchemes(
				`"UserKey":{"type":"apiKey","in":"header","name":"X-User-Key"}`,
				`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
			openAPIPaths(openAPIGet("/signed", "signedReport", `"security":[{"UserKey":[],"PartnerKey":[]}]`)))
	})
	env.writeAPIConfig(t, testAPIConfig(env.baseURL(t), profileCredentials(map[string]*config.CredentialConfig{
		"UserKey":    testCredential(apiKeyAuth("header", "X-User-Key", "user-secret")),
		"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "partner-secret")),
	})))

	c, _ := env.newCaptureCLI()
	err := c.Run([]string{"restish", "api", "auth", "get", "tapi", "--operation", "signed-report", "--print-header"})
	if err == nil {
		t.Fatal("expected operation --print-header with multiple headers to fail")
	}
	if !strings.Contains(err.Error(), "produced multiple header values") {
		t.Errorf("error = %q, want it to mention multiple header values", err)
	}
}
