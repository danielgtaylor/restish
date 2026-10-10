package cli

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	internalplugin "github.com/rest-sh/restish/v2/internal/plugin"
	"github.com/rest-sh/restish/v2/internal/request"
	"github.com/rest-sh/restish/v2/internal/spec"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
)

type forceRecordingAuth struct {
	forces []bool
}

func (h *forceRecordingAuth) Parameters() []auth.Param { return nil }

func (h *forceRecordingAuth) Authenticate(_ context.Context, req *http.Request, ac auth.AuthContext) error {
	h.forces = append(h.forces, ac.Force)
	req.Header.Set("Authorization", "Bearer token")
	return nil
}

func (h *forceRecordingAuth) SupportsForce() {}

func TestPlanOperationAuthRejectsMissingRequirementValues(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"UserOAuth": {
				Auth:      &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-User-Key", "value": "secret", "scopes": "items:read"}},
				Satisfies: []string{"items:write"},
			},
		},
	}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{{
		{ID: "UserOAuth", Needs: []string{"items:read"}},
	}}}

	_, _, err := c.planOperationAuth("svc", "default", prof, policy)
	if err == nil {
		t.Fatal("expected missing requirement value error")
	}
	if !strings.Contains(err.Error(), "do not satisfy") || !strings.Contains(err.Error(), "items:read") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanOperationAuthDerivesSatisfiesFromAuthProfileScopes(t *testing.T) {
	c := &CLI{cfg: &config.Config{
		AuthProfiles: map[string]*config.AuthConfig{
			"shared-oauth": {
				Type:   "api-key",
				Params: map[string]string{"in": "header", "name": "Authorization", "value": "Bearer token", "scopes": "items:read items:write"},
			},
		},
	}}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"UserOAuth": {AuthRef: "shared-oauth"},
		},
	}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{{
		{ID: "UserOAuth", Needs: []string{"items:read"}},
	}}}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 1 || selected[0].resolved.Ref != "shared-oauth" {
		t.Fatalf("selected = %#v handled=%v, want shared auth profile", selected, handled)
	}
}

func TestPlanOperationAuthHandlesAnonymousOnlySecurity(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Key", "value": "env:MISSING_KEY"}},
	}
	policy := &operationAuthPolicy{OptionalAuth: true}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 0 {
		t.Fatalf("selected = %#v handled=%v, want anonymous-only handling", selected, handled)
	}
}

func TestPlanOperationAuthRejectsAmbiguousProfileFallback(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Auth: &config.AuthConfig{Type: "http-basic", Params: map[string]string{"username": "u", "password": "p"}},
	}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{
		{{ID: "UserOAuth"}},
		{{ID: "PartnerKey"}},
	}}

	_, _, err := c.planOperationAuth("svc", "default", prof, policy)
	if err == nil {
		t.Fatal("expected missing credential binding error")
	}
	if !strings.Contains(err.Error(), "missing credential bindings") ||
		!strings.Contains(err.Error(), "UserOAuth") ||
		!strings.Contains(err.Error(), "PartnerKey") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanOperationAuthReportsUndeclaredSecurityScheme(t *testing.T) {
	c := &CLI{}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{{
		{ID: "BearerAuth", Kind: "unknown", Undeclared: true},
	}}}

	_, _, err := c.planOperationAuth("svc", "default", nil, policy)
	if err == nil {
		t.Fatal("expected missing profile auth error")
	}
	for _, want := range []string{
		`OpenAPI security issue: security scheme "BearerAuth" is referenced by operations but is not declared in components.securitySchemes`,
		"--rsh-auth BearerAuth",
		"restish api auth inspect svc",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
}

func TestPlanOperationAuthMissingCredentialSuggestsExplicitConfiguredOverride(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"BearerAuth": {
				Auth: &config.AuthConfig{Type: "bearer", Params: map[string]string{"token": "secret"}},
			},
		},
	}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{{
		{ID: "PartnerKey"},
	}}}

	_, _, err := c.planOperationAuth("svc", "default", prof, policy)
	if err == nil {
		t.Fatal("expected missing credential binding error")
	}
	if !strings.Contains(err.Error(), `configured credential "BearerAuth" is not declared for this operation`) ||
		!strings.Contains(err.Error(), "--rsh-auth BearerAuth") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanOperationAuthAllowsSingleRequirementProfileFallback(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Auth: &config.AuthConfig{Type: "http-basic", Params: map[string]string{"username": "u", "password": "p"}},
	}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{{
		{ID: "BasicAuth"},
	}}}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 1 || selected[0].resolved.Config != prof.Auth {
		t.Fatalf("selected = %#v handled=%v, want profile fallback", selected, handled)
	}
}

func TestPlanOperationAuthPrefersCredentialsBeforeAnonymous(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"PartnerKey": {
				Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Partner-Key", "value": "secret"}},
			},
		},
	}
	policy := &operationAuthPolicy{
		OptionalAuth: true,
		CredentialAlternatives: []spec.CredentialAlternative{{
			{ID: "PartnerKey"},
		}},
	}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 1 || selected[0].requirement.ID != "PartnerKey" {
		t.Fatalf("selected = %#v handled=%v, want PartnerKey", selected, handled)
	}
}

func TestPlanOperationAuthSkipsAlternativeWithMissingEnvParam(t *testing.T) {
	t.Setenv("READY_KEY", "ready")
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"MissingKey": {
				Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Missing", "value": "env:MISSING_KEY"}},
			},
			"ReadyKey": {
				Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Ready", "value": "env:READY_KEY"}},
			},
		},
	}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{
		{{ID: "MissingKey"}},
		{{ID: "ReadyKey"}},
	}}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 1 || selected[0].requirement.ID != "ReadyKey" {
		t.Fatalf("selected = %#v handled=%v, want ReadyKey", selected, handled)
	}
}

func TestPlanOperationAuthReportsUnresolvedEnvWhenNoAlternativeReady(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"MissingKey": {
				Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Missing", "value": "env:MISSING_KEY"}},
			},
		},
	}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{{{ID: "MissingKey"}}}}

	_, _, err := c.planOperationAuth("svc", "default", prof, policy)
	if err == nil || !strings.Contains(err.Error(), "unresolved auth params") || !strings.Contains(err.Error(), "env:MISSING_KEY") {
		t.Fatalf("error = %v, want unresolved env diagnostic", err)
	}
}

func TestPlanOperationAuthUsesAnonymousWhenOptionalCredentialMissing(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{}
	policy := &operationAuthPolicy{
		OptionalAuth: true,
		CredentialAlternatives: []spec.CredentialAlternative{{
			{ID: "PartnerKey"},
		}},
	}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 0 {
		t.Fatalf("selected = %#v handled=%v, want anonymous", selected, handled)
	}
}

func TestPlanOperationAuthUsesAnonymousWhenOptionalCredentialEnvMissing(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"PartnerKey": {
				Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Partner-Key", "value": "env:MISSING_PARTNER_KEY"}},
			},
		},
	}
	policy := &operationAuthPolicy{
		OptionalAuth: true,
		CredentialAlternatives: []spec.CredentialAlternative{{
			{ID: "PartnerKey"},
		}},
	}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 0 {
		t.Fatalf("selected = %#v handled=%v, want anonymous", selected, handled)
	}
}

func TestPlanOperationAuthUsesAnonymousBeforeProfileFallbackWhenOptionalCredentialEnvMissing(t *testing.T) {
	c := &CLI{}
	sharedAuth := &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Partner-Key", "value": "env:MISSING_PARTNER_KEY"}}
	prof := &config.ProfileConfig{
		Auth: sharedAuth,
		Credentials: map[string]*config.CredentialConfig{
			"PartnerKey": {Auth: sharedAuth},
		},
	}
	policy := &operationAuthPolicy{
		OptionalAuth: true,
		CredentialAlternatives: []spec.CredentialAlternative{{
			{ID: "PartnerKey"},
		}},
	}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 0 {
		t.Fatalf("selected = %#v handled=%v, want anonymous before profile fallback", selected, handled)
	}
}

func TestPlanOperationAuthSatisfiesMTLSWithTransportClientCertificate(t *testing.T) {
	c := &CLI{}
	policy := &operationAuthPolicy{
		CredentialAlternatives: []spec.CredentialAlternative{{
			{ID: "ClientCert", Kind: "mtls"},
		}},
		Transport: request.Options{
			ClientCertPath: "client.pem",
			ClientKeyPath:  "client-key.pem",
		},
	}

	selected, handled, err := c.planOperationAuth("svc", "default", nil, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 0 {
		t.Fatalf("selected = %#v handled=%v, want transport-only mTLS", selected, handled)
	}
}

func TestPlanOperationAuthRequiresMTLSConfiguration(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Auth: &config.AuthConfig{Type: "bearer", Params: map[string]string{"token": "not-mtls"}},
	}
	policy := &operationAuthPolicy{
		CredentialAlternatives: []spec.CredentialAlternative{{
			{ID: "ClientCert", Kind: "mtls"},
		}},
	}

	_, _, err := c.planOperationAuth("svc", "default", prof, policy)
	if err == nil {
		t.Fatal("expected missing mTLS configuration error")
	}
	for _, want := range []string{"ClientCert requires mutual TLS", "--rsh-client-cert", "--rsh-client-key", "client_cert/client_key", "tls_signer"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "profile auth fallback") {
		t.Fatalf("mTLS should not fall back to bearer profile auth: %v", err)
	}
}

func TestPlanOperationAuthOverrideSatisfiesMTLSWithTransportSigner(t *testing.T) {
	c := &CLI{}
	policy := &operationAuthPolicy{
		CredentialAlternatives: []spec.CredentialAlternative{{
			{ID: "ClientCert", Kind: "mtls"},
		}},
		Override: "ClientCert",
		Transport: request.Options{
			TLSSignerName: "pkcs11",
		},
	}

	selected, handled, err := c.planOperationAuth("svc", "default", nil, policy)
	if err != nil {
		t.Fatalf("planOperationAuth override: %v", err)
	}
	if !handled || len(selected) != 0 {
		t.Fatalf("selected = %#v handled=%v, want transport-only mTLS override", selected, handled)
	}
}

func TestPlanOperationAuthOverrideValidation(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"PartnerKey": {
				Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Partner-Key", "value": "secret"}},
			},
		},
	}
	basePolicy := []spec.CredentialAlternative{
		{{ID: "UserOAuth", Needs: []string{"items:read"}}},
		{{ID: "PartnerKey"}},
	}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, &operationAuthPolicy{
		CredentialAlternatives: basePolicy,
		Override:               "PartnerKey",
	})
	if err != nil {
		t.Fatalf("valid override: %v", err)
	}
	if !handled || len(selected) != 1 || selected[0].requirement.ID != "PartnerKey" {
		t.Fatalf("selected = %#v handled=%v, want PartnerKey", selected, handled)
	}

	_, _, err = c.planOperationAuth("svc", "default", prof, &operationAuthPolicy{
		CredentialAlternatives: basePolicy,
		Override:               "UserOAuth+PartnerKey",
	})
	if err == nil || !strings.Contains(err.Error(), "requires missing credential bindings") {
		t.Fatalf("expected missing binding error, got %v", err)
	}

	_, _, err = c.planOperationAuth("svc", "default", prof, &operationAuthPolicy{
		CredentialAlternatives: basePolicy,
		Override:               "UserOAuth",
	})
	if err == nil || !strings.Contains(err.Error(), "requires missing credential bindings") {
		t.Fatalf("expected missing binding error, got %v", err)
	}

	selected, handled, err = c.planOperationAuth("svc", "default", prof, &operationAuthPolicy{
		OptionalAuth:           true,
		CredentialAlternatives: basePolicy,
		Override:               "anonymous",
	})
	if err != nil {
		t.Fatalf("anonymous override: %v", err)
	}
	if !handled || len(selected) != 0 {
		t.Fatalf("selected = %#v handled=%v, want anonymous", selected, handled)
	}
}

func TestOperationAuthCoverageCountsOptionalAnonymousAsCallable(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"ApiKey": {
				Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-API-Key", "value": "env:MISSING_API_KEY"}},
			},
		},
	}
	ops := []spec.Operation{
		{
			ID:           "optional",
			OptionalAuth: true,
			CredentialAlternatives: []spec.CredentialAlternative{{
				{ID: "ApiKey"},
			}},
		},
		{
			ID: "required",
			CredentialAlternatives: []spec.CredentialAlternative{{
				{ID: "ApiKey"},
			}},
		},
	}

	coverage := c.operationAuthCoverage("svc", "default", prof, ops)
	if coverage.Callable != 1 || coverage.Secured != 2 {
		t.Fatalf("coverage = callable %d secured %d, want 1/2", coverage.Callable, coverage.Secured)
	}
}

func TestOperationAuthCoverageDefersConfiguredDPoPSourcePerRequest(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{Credentials: map[string]*config.CredentialConfig{
		"RealmrootOAuth": {
			Auth: &config.AuthConfig{Type: "dpop", Params: map[string]string{
				"source": "realmroot", "reference": "https://identity.example/resources/wallet",
			}},
		},
	}}
	ops := []spec.Operation{
		{ID: "readWallet", CredentialAlternatives: []spec.CredentialAlternative{{{
			ID: "RealmrootOAuth", Kind: "oauth2-dpop", Needs: []string{"wallet:read"},
		}}}},
		{ID: "pay", CredentialAlternatives: []spec.CredentialAlternative{{{
			ID: "RealmrootOAuth", Kind: "oauth2-dpop", Needs: []string{"wallet:x402:pay"},
		}}}},
	}

	coverage := c.operationAuthCoverage("wallet", "default", prof, ops)
	if coverage.Callable != 0 || coverage.SourceEvaluated != 2 || coverage.Secured != 2 {
		t.Fatalf("coverage = %#v, want 0 statically callable and 2 source-evaluated", coverage)
	}
}

func TestOperationAuthCoverageCountsProfileMTLSAsCallable(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		ClientCertPath: "client.pem",
		ClientKeyPath:  "client-key.pem",
	}
	ops := []spec.Operation{
		{
			ID: "mtls",
			CredentialAlternatives: []spec.CredentialAlternative{{
				{ID: "ClientCert", Kind: "mtls"},
			}},
		},
	}

	coverage := c.operationAuthCoverage("svc", "default", prof, ops)
	if coverage.Callable != 1 || coverage.Secured != 1 {
		t.Fatalf("coverage = callable %d secured %d, want 1/1", coverage.Callable, coverage.Secured)
	}
}

func TestPlanOperationAuthOverrideAllowsConfiguredCredentialOutsideSpec(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"BearerAuth": {
				Auth: &config.AuthConfig{Type: "bearer", Params: map[string]string{"token": "manual"}},
			},
		},
	}
	policy := &operationAuthPolicy{
		CredentialAlternatives: []spec.CredentialAlternative{{{ID: "LegacyKey"}}},
		Override:               "BearerAuth",
	}

	selected, handled, err := c.planOperationAuth("svc", "default", prof, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 1 || selected[0].requirement.ID != "BearerAuth" {
		t.Fatalf("selected = %#v handled=%v, want BearerAuth", selected, handled)
	}
}

func TestPlanOperationAuthRejectsCredentialMutationConflict(t *testing.T) {
	c := &CLI{}
	prof := &config.ProfileConfig{
		Credentials: map[string]*config.CredentialConfig{
			"A": {Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-API-Key", "value": "a"}}},
			"B": {Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "x-api-key", "value": "b"}}},
		},
	}
	policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{{
		{ID: "A"},
		{ID: "B"},
	}}}

	_, _, err := c.planOperationAuth("svc", "default", prof, policy)
	if err == nil {
		t.Fatal("expected credential mutation conflict")
	}
	if !strings.Contains(err.Error(), "both write header:x-api-key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanOperationAuthRejectsBearerAuthorizationConflicts(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth *config.AuthConfig
	}{
		{
			name: "http-basic",
			auth: &config.AuthConfig{Type: "http-basic", Params: map[string]string{
				"username": "u",
				"password": "p",
			}},
		},
		{
			name: "oauth2",
			auth: &config.AuthConfig{Type: "oauth-client-credentials", Params: map[string]string{
				"client_id":     "id",
				"client_secret": "secret",
				"token_url":     "https://auth.example.com/token",
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &CLI{}
			prof := &config.ProfileConfig{
				Credentials: map[string]*config.CredentialConfig{
					"Bearer": {Auth: &config.AuthConfig{Type: "bearer", Params: map[string]string{"token": "abc"}}},
					"Other":  {Auth: tc.auth},
				},
			}
			policy := &operationAuthPolicy{CredentialAlternatives: []spec.CredentialAlternative{{
				{ID: "Bearer"},
				{ID: "Other"},
			}}}

			_, _, err := c.planOperationAuth("svc", "default", prof, policy)
			if err == nil {
				t.Fatal("expected credential mutation conflict")
			}
			if !strings.Contains(err.Error(), "both write header:authorization") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOperationAuthCallbacksForceCapableUnauthorizedRetry(t *testing.T) {
	c := New()
	handler := &forceRecordingAuth{}
	c.AddAuthHandler("force-test", handler)
	selected := []selectedOperationAuth{{
		requirement: spec.CredentialRequirement{ID: "UserOAuth"},
		resolved: resolvedAuthConfig{
			Config: &config.AuthConfig{Type: "force-test"},
		},
	}}

	callbacks, err := c.operationAuthCallbacks("svc", "default", selected, authHandlerOptions{})
	if err != nil {
		t.Fatalf("operationAuthCallbacks: %v", err)
	}
	if callbacks.OnRequest == nil || callbacks.OnUnauthorized == nil {
		t.Fatal("expected request and unauthorized callbacks")
	}

	req, _ := http.NewRequest("GET", "https://api.example.com/items", nil)
	if err := callbacks.OnRequest(req); err != nil {
		t.Fatalf("OnRequest: %v", err)
	}
	if err := callbacks.OnUnauthorized(req); err != nil {
		t.Fatalf("OnUnauthorized: %v", err)
	}
	if len(handler.forces) != 2 || handler.forces[0] || !handler.forces[1] {
		t.Fatalf("forces = %#v, want [false true]", handler.forces)
	}
}

func TestOperationAuthCallbacksRunHookOnceForMultipleCredentials(t *testing.T) {
	c := New()
	var hookCalls atomic.Int32
	c.Hooks().AuthHookFunc = func(apiName, profileName string, rawParams map[string]string, secretKeys map[string]bool, req *http.Request) error {
		hookCalls.Add(1)
		req.Header.Set("X-Hook", "called")
		return nil
	}
	selected := []selectedOperationAuth{
		{
			requirement: spec.CredentialRequirement{ID: "FirstKey"},
			resolved: resolvedAuthConfig{
				Config: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-First-Key", "value": "one"}},
			},
		},
		{
			requirement: spec.CredentialRequirement{ID: "SecondKey"},
			resolved: resolvedAuthConfig{
				Config: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-Second-Key", "value": "two"}},
			},
		},
	}

	callbacks, err := c.operationAuthCallbacks("svc", "default", selected, authHandlerOptions{})
	if err != nil {
		t.Fatalf("operationAuthCallbacks: %v", err)
	}
	req, _ := http.NewRequest("GET", "https://api.example.com/items", nil)
	if err := callbacks.OnRequest(req); err != nil {
		t.Fatalf("OnRequest: %v", err)
	}
	if got := hookCalls.Load(); got != 1 {
		t.Fatalf("hook calls = %d, want 1", got)
	}
	if req.Header.Get("X-First-Key") != "one" || req.Header.Get("X-Second-Key") != "two" || req.Header.Get("X-Hook") != "called" {
		t.Fatalf("headers after operation auth = %#v", req.Header)
	}
}

func TestOperationAuthPreservesAPIKeyHeaderCase(t *testing.T) {
	c := &CLI{cfg: &config.Config{APIs: map[string]*config.APIConfig{
		"svc": {PreserveHeaderCase: true},
	}}}
	selected := []selectedOperationAuth{{
		requirement: spec.CredentialRequirement{ID: "SourceSystem"},
		resolved: resolvedAuthConfig{
			Config: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-SourceSystem", "value": "secret"}},
		},
	}}

	callbacks, err := c.operationAuthCallbacks("svc", "default", selected, authHandlerOptions{})
	if err != nil {
		t.Fatalf("operationAuthCallbacks: %v", err)
	}
	req, _ := http.NewRequest("GET", "https://api.example.com/items", nil)
	if err := callbacks.OnRequest(req); err != nil {
		t.Fatalf("OnRequest: %v", err)
	}
	if got := req.Header["X-SourceSystem"]; len(got) != 1 || got[0] != "secret" {
		t.Fatalf("X-SourceSystem = %#v, want preserved operation auth header", got)
	}
	if got := req.Header["X-Sourcesystem"]; len(got) != 0 {
		t.Fatalf("canonicalized operation auth header was left behind: %#v", req.Header)
	}
}

func TestOperationAuthPreserveHeaderCaseDoesNotRewriteManualHeader(t *testing.T) {
	c := &CLI{cfg: &config.Config{APIs: map[string]*config.APIConfig{
		"svc": {PreserveHeaderCase: true},
	}}}
	selected := []selectedOperationAuth{{
		requirement: spec.CredentialRequirement{ID: "SourceSystem"},
		resolved: resolvedAuthConfig{
			Config: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "X-SourceSystem", "value": "secret"}},
		},
	}}

	callbacks, err := c.operationAuthCallbacks("svc", "default", selected, authHandlerOptions{})
	if err != nil {
		t.Fatalf("operationAuthCallbacks: %v", err)
	}
	req, _ := http.NewRequest("GET", "https://api.example.com/items", nil)
	req.Header["x-sourcesystem"] = []string{"manual"}
	if err := callbacks.OnRequest(req); err != nil {
		t.Fatalf("OnRequest: %v", err)
	}
	if got := req.Header["x-sourcesystem"]; len(got) != 1 || got[0] != "manual" {
		t.Fatalf("x-sourcesystem = %#v, want manual header with user casing", got)
	}
	if got := req.Header["X-SourceSystem"]; len(got) != 0 {
		t.Fatalf("manual operation auth header was rewritten: %#v", req.Header)
	}
	if got := req.Header["X-Sourcesystem"]; len(got) != 0 {
		t.Fatalf("operation auth added canonicalized duplicate: %#v", req.Header)
	}
}

func TestPlanOperationAuthUsesResolverOnlyAfterConfiguredCredentialsFail(t *testing.T) {
	resolver := internalplugin.Plugin{Path: "/resolver", Manifest: pluginwire.Manifest{Name: "vault", Hooks: []string{"auth-resolver", "auth"}}}
	c := &CLI{pluginsByHook: map[string][]internalplugin.Plugin{"auth-resolver": {resolver}}}
	var received pluginwire.AuthResolverInput
	c.hooks.AuthResolverHookFunc = func(_ internalplugin.Plugin, input pluginwire.AuthResolverInput) (bool, error) {
		received = input
		return true, nil
	}
	policy := &operationAuthPolicy{
		Context: context.Background(), Method: http.MethodGet, URL: "https://api.example.com/v1/projects",
		CredentialAlternatives: []spec.CredentialAlternative{{{ID: "OAuth", Kind: "oauth2", Needs: []string{"projects:read"}}}},
	}
	selected, handled, err := c.planOperationAuth("projects", "default", nil, policy)
	if err != nil {
		t.Fatalf("planOperationAuth: %v", err)
	}
	if !handled || len(selected) != 1 || selected[0].resolver == nil || selected[0].resolver.Manifest.Name != "vault" {
		t.Fatalf("selected resolver = %#v, handled = %v", selected, handled)
	}
	if received.Request.URI != policy.URL || received.Request.Method != http.MethodGet || len(received.Requirements) != 1 || received.Requirements[0].Needs[0] != "projects:read" {
		t.Fatalf("resolver input = %#v", received)
	}

	called := false
	c.hooks.AuthResolverHookFunc = func(_ internalplugin.Plugin, _ pluginwire.AuthResolverInput) (bool, error) {
		called = true
		return true, nil
	}
	profile := &config.ProfileConfig{Credentials: map[string]*config.CredentialConfig{
		"OAuth": {Auth: &config.AuthConfig{Type: "api-key", Params: map[string]string{"in": "header", "name": "Authorization", "value": "Bearer configured"}}, Satisfies: []string{"projects:read"}},
	}}
	selected, handled, err = c.planOperationAuth("projects", "default", profile, policy)
	if err != nil || !handled || len(selected) != 1 {
		t.Fatalf("configured plan = %#v, handled = %v, err = %v", selected, handled, err)
	}
	if called || selected[0].resolver != nil {
		t.Fatal("resolver must not override a configured credential")
	}
}

func TestAPIAuthInspectDefersUnconfiguredOperationToResolver(t *testing.T) {
	var out strings.Builder
	c := &CLI{
		Stdout: &out,
		pluginsByHook: map[string][]internalplugin.Plugin{"auth-resolver": {{
			Manifest: pluginwire.Manifest{Name: "vault", Hooks: []string{"auth-resolver", "auth"}},
		}}},
	}
	op := spec.Operation{
		ID: "listProjects",
		CredentialAlternatives: []spec.CredentialAlternative{
			{{ID: "OIDC", Kind: "openid", Needs: []string{"projects:read"}}},
			{{ID: "Session", Kind: "api-key"}},
		},
	}
	if !c.operationAuthDeferredToResolver("projects", &config.ProfileConfig{}, op) {
		t.Fatal("expected operation auth inspection to defer to the resolver")
	}
	c.printResolverDeferredOperationAuth(op)
	got := out.String()
	for _, want := range []string{
		"Operation: listProjects",
		"Auth: resolver evaluated at request time",
		"OIDC (needs projects:read)",
		"Session",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want %q", got, want)
		}
	}
}

func TestAPIAuthInspectDoesNotHideConfiguredCredentialErrorsBehindResolver(t *testing.T) {
	c := &CLI{pluginsByHook: map[string][]internalplugin.Plugin{"auth-resolver": {{
		Manifest: pluginwire.Manifest{Name: "vault", Hooks: []string{"auth-resolver", "auth"}},
	}}}}
	op := spec.Operation{
		ID: "listProjects",
		CredentialAlternatives: []spec.CredentialAlternative{{{
			ID: "OIDC", Kind: "oauth2", Needs: []string{"projects:read"},
		}}},
	}
	profile := &config.ProfileConfig{Credentials: map[string]*config.CredentialConfig{
		"OIDC": {Auth: &config.AuthConfig{Type: "bearer", Params: map[string]string{"token": "env:MISSING"}}},
	}}
	if c.operationAuthDeferredToResolver("projects", profile, op) {
		t.Fatal("configured credential inspection must retain strict readiness errors")
	}
}

func TestPlanOperationAuthRejectsAmbiguousResolvers(t *testing.T) {
	c := &CLI{pluginsByHook: map[string][]internalplugin.Plugin{"auth-resolver": {
		{Manifest: pluginwire.Manifest{Name: "first"}},
		{Manifest: pluginwire.Manifest{Name: "second"}},
	}}}
	c.hooks.AuthResolverHookFunc = func(_ internalplugin.Plugin, _ pluginwire.AuthResolverInput) (bool, error) { return true, nil }
	_, _, err := c.planOperationAuth("projects", "default", nil, &operationAuthPolicy{
		Method: http.MethodGet, URL: "https://api.example.com/projects",
		CredentialAlternatives: []spec.CredentialAlternative{{{ID: "OAuth", Kind: "oauth2"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple auth resolver plugins") {
		t.Fatalf("error = %v", err)
	}
}
