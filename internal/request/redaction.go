package request

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/rest-sh/restish/v2/internal/secrets"
)

type credentialRedactionContextKey struct{}

type credentialRedactionTargets struct {
	headers     map[string]bool
	queryParams map[string]bool
	cookies     map[string]bool
	body        bool
}

func redactionTargets(req *http.Request, create bool) *credentialRedactionTargets {
	if req == nil {
		return nil
	}
	if targets, ok := req.Context().Value(credentialRedactionContextKey{}).(*credentialRedactionTargets); ok {
		return targets
	}
	if !create {
		return nil
	}
	targets := &credentialRedactionTargets{}
	*req = *req.WithContext(context.WithValue(req.Context(), credentialRedactionContextKey{}, targets))
	return targets
}

// MarkCredentialHeader records that a request header receives a credential
// value from configured auth, even when the header name is not generally secret.
func MarkCredentialHeader(req *http.Request, name string) {
	name = http.CanonicalHeaderKey(strings.TrimSpace(name))
	if name == "" {
		return
	}
	targets := redactionTargets(req, true)
	if targets.headers == nil {
		targets.headers = map[string]bool{}
	}
	targets.headers[name] = true
}

// MarkCredentialQueryParam records that a request query parameter receives a
// credential value from configured auth.
func MarkCredentialQueryParam(req *http.Request, name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return
	}
	targets := redactionTargets(req, true)
	if targets.queryParams == nil {
		targets.queryParams = map[string]bool{}
	}
	targets.queryParams[name] = true
}

// MarkCredentialCookie records that a request cookie receives a credential
// value from configured auth.
func MarkCredentialCookie(req *http.Request, name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return
	}
	targets := redactionTargets(req, true)
	if targets.cookies == nil {
		targets.cookies = map[string]bool{}
	}
	targets.cookies[name] = true
}

// MarkCredentialBody records that a request body contains a credential value.
func MarkCredentialBody(req *http.Request) {
	redactionTargets(req, true).body = true
}

// IsMarkedCredentialHeader reports whether a request header was marked as
// carrying a credential by Restish auth setup.
func IsMarkedCredentialHeader(req *http.Request, name string) bool {
	targets := redactionTargets(req, false)
	return targets != nil && targets.headers[http.CanonicalHeaderKey(name)]
}

// IsMarkedCredentialQueryParam reports whether a query parameter was marked as
// carrying a credential by Restish auth setup.
func IsMarkedCredentialQueryParam(req *http.Request, name string) bool {
	targets := redactionTargets(req, false)
	return targets != nil && targets.queryParams[strings.ToLower(name)]
}

// IsMarkedCredentialCookie reports whether a cookie was marked as carrying a
// credential by Restish auth setup.
func IsMarkedCredentialCookie(req *http.Request, name string) bool {
	targets := redactionTargets(req, false)
	return targets != nil && targets.cookies[strings.ToLower(name)]
}

// IsMarkedCredentialBody reports whether a request body contains a credential.
func IsMarkedCredentialBody(req *http.Request) bool {
	targets := redactionTargets(req, false)
	return targets != nil && targets.body
}

// RedactedRequestURL returns req.URL with generic secrets and auth-marked query
// credentials redacted.
func RedactedRequestURL(req *http.Request) string {
	if req == nil {
		return ""
	}
	return redactedURL(req.URL, func(name, _ string) bool {
		return IsMarkedCredentialQueryParam(req, name)
	})
}

// RedactedRequestURI returns req.URL.RequestURI with generic secrets and
// auth-marked query credentials redacted.
func RedactedRequestURI(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	copyURL := *req.URL
	redactURLQuery(&copyURL, func(name, _ string) bool {
		return IsMarkedCredentialQueryParam(req, name)
	})
	return copyURL.RequestURI()
}

func redactedURL(u *url.URL, isMarkedCredential func(name, value string) bool) string {
	if u == nil {
		return ""
	}
	copyURL := *u
	if copyURL.User != nil {
		copyURL.User = url.User("redacted")
	}
	redactURLQuery(&copyURL, isMarkedCredential)
	return copyURL.String()
}

func redactURLQuery(u *url.URL, isMarkedCredential func(name, value string) bool) {
	q := u.Query()
	for name, values := range q {
		for i, value := range values {
			if secrets.IsQueryParamValue(name, value) || (isMarkedCredential != nil && isMarkedCredential(name, value)) {
				values[i] = "<redacted>"
			}
		}
		q[name] = values
	}
	u.RawQuery = q.Encode()
}
