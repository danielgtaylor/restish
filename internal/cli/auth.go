package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	authpkg "github.com/rest-sh/restish/v2/internal/auth"
	"github.com/rest-sh/restish/v2/internal/output"
	"github.com/rest-sh/restish/v2/internal/procutil"
	"github.com/rest-sh/restish/v2/internal/request"
	"github.com/rest-sh/restish/v2/internal/secrets"
	"github.com/spf13/cobra"
)

type authHandlerOptions struct {
	NoBrowser bool
	Verbose   bool
}

type authCallbacks struct {
	OnRequest      func(*http.Request) error
	OnRetryRequest func(*http.Request) error
	OnUnauthorized func(*http.Request) error
}

type resolvedAuthConfig struct {
	Config   *config.AuthConfig
	Ref      string
	CacheKey string
}

type cliPrompter struct {
	cli *CLI
	ctx context.Context
}

func (p cliPrompter) Prompt(prompt string) (string, error) {
	return p.cli.Prompt(p.ctx, prompt)
}
func (p cliPrompter) PromptSecret(prompt string) (string, error) {
	return p.cli.Secret(p.ctx, prompt)
}

// authHandlerFor returns the Handler for the given AuthConfig.
// Custom handlers registered via CLI.AddAuthHandler take precedence over
// built-in handlers.
func (c *CLI) authHandlerOptionsFromCmd(cmd *cobra.Command) (authHandlerOptions, error) {
	if cmd == nil {
		return authHandlerOptions{}, nil
	}
	gf := globalFlagsFromContext(requestContext(cmd))
	return authHandlerOptions{NoBrowser: gf.NoBrowser, Verbose: gf.Verbose > 0}, nil
}

func (c *CLI) authHandlerFor(ac *config.AuthConfig, opts authHandlerOptions) (auth.Handler, error) {
	if c.customAuthHandlers != nil {
		if h, ok := c.customAuthHandlers[ac.Type]; ok {
			return h, nil
		}
	}
	switch ac.Type {
	case "api-key":
		return &authpkg.APIKey{}, nil
	case "bearer":
		return &authpkg.Bearer{}, nil
	case "http-basic":
		return &authpkg.HTTPBasic{}, nil
	case "dpop":
		return &authpkg.DPoP{Source: pluginDPoPCredentialSource{cli: c}}, nil
	case "oauth-client-credentials":
		return &authpkg.ClientCredentials{
			Cache:      auth.NewTokenCache(c.tokenCachePath()),
			HTTPClient: &http.Client{Transport: c.baseHTTPTransport()},
		}, nil
	case "oauth-authorization-code":
		return &authpkg.AuthorizationCode{
			Cache:                auth.NewTokenCache(c.tokenCachePath()),
			HTTPClient:           &http.Client{Transport: c.baseHTTPTransport()},
			Stderr:               c.Stderr,
			CanPrompt:            c.canPromptCode(),
			NoBrowser:            opts.NoBrowser,
			Verbose:              opts.Verbose,
			CallbackSuccessColor: output.ThemeTokenColor("status_2xx"),
			CallbackFailureColor: output.ThemeTokenColor("status_error"),
		}, nil
	case "oauth-device-code":
		return &authpkg.DeviceCode{
			Cache:      auth.NewTokenCache(c.tokenCachePath()),
			HTTPClient: &http.Client{Transport: c.baseHTTPTransport()},
			Stderr:     c.Stderr,
		}, nil
	case "external-tool":
		return &authpkg.ExternalTool{Stderr: c.Stderr}, nil
	default:
		return nil, fmt.Errorf("unknown auth type %q; supported: api-key, bearer, http-basic, dpop, oauth-client-credentials, oauth-authorization-code, oauth-device-code, external-tool", ac.Type)
	}
}

// authOnRequest returns auth callbacks for the given profile's auth config,
// or zero values when no auth is configured.
func (c *CLI) authOnRequest(apiName, profileName string, prof *config.ProfileConfig, opts authHandlerOptions) authCallbacks {
	// Determine whether built-in auth is configured.
	var callbacks authCallbacks
	resolvedAuth, err := c.resolveProfileAuth(apiName, profileName, prof)
	if err != nil {
		callbacks.OnRequest = func(*http.Request) error { return err }
		return callbacks
	}
	if resolvedAuth.Config != nil {
		handler, err := c.authHandlerFor(resolvedAuth.Config, opts)
		if err != nil {
			callbacks.OnRequest = func(*http.Request) error { return err }
			return callbacks
		}
		rawParams := resolvedAuth.Config.Params
		secretKeys := make(map[string]bool)
		for _, p := range handler.Parameters() {
			if p.Secret {
				secretKeys[p.Name] = true
			}
		}
		apply := func(req *http.Request) error {
			if c.applyCachedOAuthClientCredentials(req, resolvedAuth.Config.Type, resolvedAuth.CacheKey, apiName, profileName, false) {
				return c.runAuthHookPlugins(apiName, profileName, rawParams, secretKeys, req)
			}
			params, err := c.buildAuthParams(rawParams)
			if err != nil {
				return err
			}
			if resolvedAuth.Config.Type == "external-tool" {
				if err := c.ensureExternalToolApproved(req.Context(), apiName, profileName, params["commandline"]); err != nil {
					return err
				}
			}
			if err := c.ensureOAuthAuthorizationCodeReady(resolvedAuth.Config.Type, resolvedAuth.CacheKey, apiName, profileName); err != nil {
				return err
			}
			preserveInsertedHeader := c.apiPreservesHeaderCase(apiName) && !authHeaderPresent(req.Header, resolvedAuth.Config.Type, params)
			if err := handler.Authenticate(req.Context(), req, c.authContext(req.Context(), apiName, profileName, params, resolvedAuth.CacheKey, false)); err != nil {
				return err
			}
			if preserveInsertedHeader {
				preserveAuthHeaderCase(req, resolvedAuth.Config.Type, params)
			}
			markAuthCredentialTargets(req, resolvedAuth.Config.Type, params)
			return c.runAuthHookPlugins(apiName, profileName, rawParams, secretKeys, req)
		}
		callbacks.OnRequest = apply
		if _, ok := handler.(auth.RequestBound); ok {
			callbacks.OnRetryRequest = apply
		}
		if _, ok := handler.(auth.ForceCapable); ok {
			callbacks.OnUnauthorized = func(req *http.Request) error {
				if c.applyCachedOAuthClientCredentials(req, resolvedAuth.Config.Type, resolvedAuth.CacheKey, apiName, profileName, true) {
					return c.runAuthHookPlugins(apiName, profileName, rawParams, secretKeys, req)
				}
				params, err := c.buildAuthParams(rawParams)
				if err != nil {
					return err
				}
				if resolvedAuth.Config.Type == "external-tool" {
					if err := c.ensureExternalToolApproved(req.Context(), apiName, profileName, params["commandline"]); err != nil {
						return err
					}
				}
				if err := c.ensureOAuthAuthorizationCodeReady(resolvedAuth.Config.Type, resolvedAuth.CacheKey, apiName, profileName); err != nil {
					return err
				}
				preserveInsertedHeader := c.apiPreservesHeaderCase(apiName) && !authHeaderPresent(req.Header, resolvedAuth.Config.Type, params)
				if err := handler.Authenticate(req.Context(), req, c.authContext(req.Context(), apiName, profileName, params, resolvedAuth.CacheKey, true)); err != nil {
					return err
				}
				if preserveInsertedHeader {
					preserveAuthHeaderCase(req, resolvedAuth.Config.Type, params)
				}
				markAuthCredentialTargets(req, resolvedAuth.Config.Type, params)
				return c.runAuthHookPlugins(apiName, profileName, rawParams, secretKeys, req)
			}
		}
	}

	// Auth hook plugins run even when no built-in auth is configured.
	hookPlugins := c.pluginsByHook["auth"]
	if callbacks.OnRequest == nil && len(hookPlugins) == 0 {
		return callbacks
	}
	if callbacks.OnRequest != nil {
		return callbacks
	}
	// No built-in auth but hook plugins exist.
	callbacks.OnRequest = func(req *http.Request) error {
		return c.runAuthHookPlugins(apiName, profileName, nil, nil, req)
	}
	return callbacks
}

func (c *CLI) apiPreservesHeaderCase(apiName string) bool {
	return c != nil && c.cfg != nil && c.cfg.APIs != nil && c.cfg.APIs[apiName] != nil && c.cfg.APIs[apiName].PreserveHeaderCase
}

func preserveAuthHeaderCase(req *http.Request, authType string, params map[string]string) {
	if req == nil || authType != "api-key" || strings.ToLower(strings.TrimSpace(params["in"])) != "header" {
		return
	}
	name := strings.TrimSpace(params["name"])
	if name == "" {
		return
	}
	var values []string
	for existing, existingValues := range req.Header {
		if strings.EqualFold(existing, name) {
			values = append(values, existingValues...)
			delete(req.Header, existing)
		}
	}
	if len(values) > 0 {
		req.Header[name] = values
	}
}

func authHeaderPresent(header http.Header, authType string, params map[string]string) bool {
	if authType != "api-key" || strings.ToLower(strings.TrimSpace(params["in"])) != "header" {
		return false
	}
	return preservedHeaderValue(header, strings.TrimSpace(params["name"])) != ""
}

func preservedHeaderValue(header http.Header, name string) string {
	if value := header.Get(name); value != "" {
		return value
	}
	for existing, values := range header {
		if strings.EqualFold(existing, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func markAuthCredentialTargets(req *http.Request, authType string, params map[string]string) {
	if req == nil || authType != "api-key" {
		return
	}
	location := strings.ToLower(strings.TrimSpace(params["in"]))
	name := strings.TrimSpace(params["name"])
	switch location {
	case "header":
		request.MarkCredentialHeader(req, name)
	case "query":
		request.MarkCredentialQueryParam(req, name)
	case "cookie":
		request.MarkCredentialCookie(req, name)
	}
}

func (c *CLI) applyCachedOAuthClientCredentials(req *http.Request, authType string, cacheKey, apiName, profileName string, force bool) bool {
	if force || authType != "oauth-client-credentials" {
		return false
	}
	token := c.cachedOAuthToken(authType, cacheKey, apiName, profileName)
	if token == "" {
		return false
	}
	if preservedHeaderValue(req.Header, "Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return true
}

func (c *CLI) cachedOAuthClientCredentialsToken(authType string, cacheKey, apiName, profileName string) string {
	if authType != "oauth-client-credentials" {
		return ""
	}
	return c.cachedOAuthToken(authType, cacheKey, apiName, profileName)
}

func (c *CLI) cachedOAuthToken(authType string, cacheKey, apiName, profileName string) string {
	cached := c.cachedOAuthTokenEntry(authType, cacheKey, apiName, profileName)
	if cached == nil || cached.IsExpired() {
		return ""
	}
	return cached.AccessToken
}

func (c *CLI) cachedOAuthTokenEntry(authType string, cacheKey, apiName, profileName string) *auth.CachedToken {
	switch authType {
	case "oauth-authorization-code", "oauth-client-credentials", "oauth-device-code":
	default:
		return nil
	}
	if cacheKey == "" {
		cacheKey = c.apiCacheNamespace(apiName, profileName)
	}
	if cacheKey == ":" || cacheKey == "" {
		return nil
	}
	tc := auth.NewTokenCache(c.tokenCachePath())
	cached, err := tc.Get(cacheKey)
	if err != nil {
		return nil
	}
	if cached != nil {
		return cached
	}
	// The cache key format changed from "apiName:profileName" to "oauth:HASH" at
	// some point, silently invalidating existing tokens on upgrade. Fall back to
	// the legacy key here; if found, migrate it to the new key and drop the old one.
	legacyKey := c.apiCacheNamespace(apiName, profileName)
	if legacyKey == cacheKey || legacyKey == ":" || legacyKey == "" {
		return nil
	}
	legacy, err := tc.Get(legacyKey)
	if err != nil || legacy == nil {
		return nil
	}
	if setErr := tc.Set(cacheKey, *legacy); setErr == nil {
		_ = tc.Delete(legacyKey)
	}
	return legacy
}

func (c *CLI) cachedOAuthAuthCodeUsable(authType, cacheKey, apiName, profileName string) bool {
	if authType != "oauth-authorization-code" {
		return false
	}
	cached := c.cachedOAuthTokenEntry(authType, cacheKey, apiName, profileName)
	if cached == nil {
		return false
	}
	return cached.AccessToken != "" && (!cached.IsExpired() || cached.RefreshToken != "")
}

func (c *CLI) ensureOAuthAuthorizationCodeReady(authType, cacheKey, apiName, profileName string) error {
	if authType != "oauth-authorization-code" {
		return nil
	}
	if c.cachedOAuthAuthCodeUsable(authType, cacheKey, apiName, profileName) {
		return nil
	}
	if c.canPromptCode() {
		return nil
	}
	return fmt.Errorf("oauth-authorization-code credential for API %q profile %q has no cached access token; rerun from an interactive terminal to complete OAuth authorization before sending the request", apiName, profileName)
}

func (c *CLI) resolveProfileAuth(apiName, profileName string, prof *config.ProfileConfig) (resolvedAuthConfig, error) {
	if prof == nil {
		return resolvedAuthConfig{}, nil
	}
	if prof.Auth != nil && prof.AuthRef != "" {
		return resolvedAuthConfig{}, fmt.Errorf("profile %q of API %q has both auth and auth_ref", profileName, apiName)
	}
	if prof.AuthRef == "" {
		cacheKey := c.apiCacheNamespace(apiName, profileName)
		if prof.Auth != nil {
			if relativeKey := inlineAuthCacheKey(cacheKey, prof.Auth, c.authBaseURL(apiName, profileName)); relativeKey != "" {
				cacheKey = relativeKey
			}
		}
		return resolvedAuthConfig{
			Config:   prof.Auth,
			CacheKey: cacheKey,
		}, nil
	}
	if c.cfg == nil || c.cfg.AuthProfiles == nil || c.cfg.AuthProfiles[prof.AuthRef] == nil {
		return resolvedAuthConfig{}, fmt.Errorf("profile %q of API %q references unknown auth profile %q", profileName, apiName, prof.AuthRef)
	}
	ac := c.cfg.AuthProfiles[prof.AuthRef]
	return resolvedAuthConfig{
		Config:   ac,
		Ref:      prof.AuthRef,
		CacheKey: sharedAuthCacheKey(prof.AuthRef, ac, c.authBaseURL(apiName, profileName)),
	}, nil
}

// buildAuthParams returns a copy of the user-supplied auth params with late
// secret sources resolved.
func (c *CLI) buildAuthParams(rawParams map[string]string) (map[string]string, error) {
	params := make(map[string]string, len(rawParams))
	for k, v := range rawParams {
		resolved, err := c.resolveAuthParam(v)
		if err != nil {
			return nil, fmt.Errorf("auth param %q: %w", k, err)
		}
		params[k] = resolved
	}
	return params, nil
}

func (c *CLI) authParamsReady(rawParams map[string]string) error {
	var missing []string
	for k, v := range rawParams {
		if !strings.HasPrefix(v, "env:") {
			continue
		}
		name := strings.TrimPrefix(v, "env:")
		if name == "" {
			missing = append(missing, k+" (missing env variable name)")
			continue
		}
		if resolved, ok := os.LookupEnv(name); !ok || resolved == "" {
			missing = append(missing, k+" (env:"+name+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%s", strings.Join(missing, ", "))
	}
	return nil
}

func (c *CLI) resolvedAuthParamsReady(resolved resolvedAuthConfig, apiName, profileName string) error {
	if resolved.Config == nil {
		return nil
	}
	if c.cachedOAuthClientCredentialsToken(resolved.Config.Type, resolved.CacheKey, apiName, profileName) != "" {
		return nil
	}
	return c.authParamsReady(resolved.Config.Params)
}

func (c *CLI) resolveAuthParam(value string) (string, error) {
	switch {
	case strings.HasPrefix(value, "env:"):
		name := strings.TrimPrefix(value, "env:")
		if name == "" {
			return "", fmt.Errorf("env secret source is missing a variable name")
		}
		resolved, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("environment variable %s is not set", name)
		}
		return resolved, nil
	case strings.HasPrefix(value, "command:"):
		commandLine := strings.TrimSpace(strings.TrimPrefix(value, "command:"))
		if commandLine == "" {
			return "", fmt.Errorf("command secret source is empty")
		}
		return c.runSecretCommand(commandLine)
	default:
		return value, nil
	}
}

func (c *CLI) runSecretCommand(commandLine string) (string, error) {
	parent := c.runCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	cmd := procutil.ShellCommand(ctx, commandLine)
	procutil.ConfigureCommandTreeKill(ctx, cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, limit: 4096}
	out, err := cmd.Output()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.Canceled) {
			return "", ctxErr
		}
		return "", fmt.Errorf("secret command timed out: %w", ctxErr)
	}
	if err != nil {
		excerpt := strings.TrimSpace(stderr.String())
		if excerpt != "" {
			return "", fmt.Errorf("secret command failed: %w: stderr: %s", err, redactDiagnosticSecretText(excerpt))
		}
		return "", fmt.Errorf("secret command failed: %w", err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func (c *CLI) authContext(ctx context.Context, apiName, profileName string, params map[string]string, cacheKey string, force bool) auth.AuthContext {
	return auth.AuthContext{
		APIName:     apiName,
		ProfileName: profileName,
		BaseURL:     c.authBaseURL(apiName, profileName),
		CacheKey:    cacheKey,
		Params:      params,
		TokenStore:  auth.NewTokenCache(c.tokenCachePath()),
		Prompter:    cliPrompter{cli: c, ctx: ctx},
		Stderr:      c.Stderr,
		HTTPClient:  c.authHTTPClient(ctx),
		Logger:      log.New(c.Stderr, "", 0),
		Force:       force,
	}
}

func (c *CLI) authBaseURL(apiName, profileName string) string {
	if c.cfg == nil || c.cfg.APIs == nil {
		return ""
	}
	api := c.cfg.APIs[apiName]
	if api == nil {
		return ""
	}
	return effectiveProfileBaseURL(api, profileName)
}

func sharedAuthCacheKey(ref string, ac *config.AuthConfig, baseURL string) string {
	if ac == nil {
		return ""
	}
	if key := ac.Params["cache_key"]; key != "" {
		return "auth_profile:" + ref + ":" + key
	}
	relevant := map[string]string{"type": ac.Type}
	for _, name := range []string{"audience", "authorize_url", "client_id", "device_authorization_url", "issuer_url", "resource", "scopes", "token_url"} {
		if value := ac.Params[name]; value != "" {
			relevant[name] = value
		}
	}
	if baseURL != "" && authConfigUsesRelativeOAuthEndpoint(ac) {
		relevant["base_url"] = baseURL
	}
	var keys []string
	for key := range relevant {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(relevant[key])
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "auth_profile:" + ref + ":" + hex.EncodeToString(sum[:8])
}

func inlineAuthCacheKey(baseKey string, ac *config.AuthConfig, baseURL string) string {
	if ac == nil {
		return ""
	}
	// Relative endpoints: key on the resolved base URL so all APIs served by
	// the same host share one token cache entry.
	if authConfigUsesRelativeOAuthEndpoint(ac) && baseURL != "" {
		sum := sha256.Sum256([]byte(baseURL))
		return baseKey + ":base_url:" + hex.EncodeToString(sum[:8])
	}
	// Absolute endpoints: for oauth-authorization-code, key on token_url or
	// issuer_url plus token request parameters so APIs pointing at the same
	// identity provider share a token only when the issued token shape is the
	// same.
	if ac.Type == "oauth-authorization-code" {
		material, ok := inlineAuthCodeCacheKeyMaterial(ac)
		if ok {
			sum := sha256.Sum256([]byte(material))
			return "oauth:" + hex.EncodeToString(sum[:8])
		}
	}
	return ""
}

func inlineAuthCodeCacheKeyMaterial(ac *config.AuthConfig) (string, bool) {
	tokenURL := ac.Params["token_url"]
	issuerURL := ac.Params["issuer_url"]
	clientID := ac.Params["client_id"]
	if clientID == "" {
		return "", false
	}
	if !absoluteOAuthCacheKeyAnchor(tokenURL) && !absoluteOAuthCacheKeyAnchor(issuerURL) {
		return "", false
	}
	relevant := map[string]string{"type": ac.Type}
	for key, value := range ac.Params {
		if value == "" || inlineAuthCodeCacheKeyIgnoresParam(key) {
			continue
		}
		relevant[key] = value
	}
	var keys []string
	for key := range relevant {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(relevant[key])
		b.WriteByte('\n')
	}
	return b.String(), true
}

func absoluteOAuthCacheKeyAnchor(value string) bool {
	if value == "" {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && u.IsAbs()
}

func inlineAuthCodeCacheKeyIgnoresParam(key string) bool {
	switch key {
	case "_base_url", "_cache_key", "auth_method", "cache_key", "client_secret",
		"callback_error_html", "callback_success_html",
		"redirect_cert", "redirect_key", "redirect_path", "redirect_port", "redirect_scheme":
		return true
	default:
		return false
	}
}

func authConfigUsesRelativeOAuthEndpoint(ac *config.AuthConfig) bool {
	if ac == nil {
		return false
	}
	for _, name := range []string{"authorize_url", "device_authorization_url", "token_url"} {
		if value := ac.Params[name]; value != "" && isRelativeOAuthEndpointValue(value) {
			return true
		}
	}
	return false
}

func isRelativeOAuthEndpointValue(value string) bool {
	u, err := url.Parse(value)
	return err == nil && !u.IsAbs() && u.Host == ""
}

type limitedWriter struct {
	w     *bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.w != nil && w.limit > w.w.Len() {
		remaining := w.limit - w.w.Len()
		if remaining > len(p) {
			remaining = len(p)
		}
		if remaining > 0 {
			_, _ = w.w.Write(p[:remaining])
		}
	}
	return len(p), nil
}

func redactDiagnosticSecretText(value string) string {
	return secrets.RedactDiagnosticText(value)
}

func (c *CLI) authHTTPClient(ctx context.Context) *http.Client {
	gf := globalFlagsFromContext(ctx)
	tlsMinVersion, _ := request.TLSVersionFromString(gf.TLSMinVersion)
	return &http.Client{Transport: request.BuildTransport(request.Options{
		Transport:     c.baseHTTPTransport(),
		Insecure:      gf.Insecure,
		CACertPath:    gf.CACert,
		TLSMinVersion: tlsMinVersion,
	})}
}

func profileNames(profiles map[string]*config.ProfileConfig) string {
	if len(profiles) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// tokenCachePath returns the effective path for the token cache file.
func (c *CLI) tokenCachePath() string {
	if c.hooks.TokenCachePath != "" {
		return c.hooks.TokenCachePath
	}
	return c.paths().TokenCache()
}

func (c *CLI) canPromptCode() bool {
	if c.hooks.PassReader != nil {
		return true
	}
	return output.IsTerminalReader(c.Stdin)
}
