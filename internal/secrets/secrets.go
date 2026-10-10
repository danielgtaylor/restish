// Package secrets centralizes the small allow-lists Restish uses to recognize
// credential-bearing names at trust boundaries.
package secrets

import (
	"net/http"
	"net/url"
	"strings"
)

// HeaderNames contains canonical HTTP header names that commonly carry
// credentials. Matching is exact after http.CanonicalHeaderKey.
var HeaderNames = map[string]bool{
	"Authorization":             true,
	"Cookie":                    true,
	"Dpop":                      true,
	"Api-Key":                   true,
	"Ocp-Apim-Subscription-Key": true,
	"Proxy-Authorization":       true,
	"Set-Cookie":                true,
	"X-Api-Key":                 true,
	"X-Api-Token":               true,
	"X-Auth-Token":              true,
	"X-Secret":                  true,
}

// QueryParamNames contains lower-case query parameter names that commonly
// carry credentials. Matching is exact after strings.ToLower.
var QueryParamNames = map[string]bool{
	"access_token":     true,
	"refresh_token":    true,
	"id_token":         true,
	"token":            true,
	"api_key":          true,
	"apikey":           true,
	"client_secret":    true,
	"password":         true,
	"secret":           true,
	"subscription-key": true,
}

var ambiguousQueryParamNames = map[string]bool{
	"auth": true,
	"key":  true,
}

// JSONBodyKeys contains lower-case JSON object keys that should be redacted in
// verbose body logging.
var JSONBodyKeys = map[string]bool{
	"access_token":        true,
	"refresh_token":       true,
	"id_token":            true,
	"token":               true,
	"api_key":             true,
	"apikey":              true,
	"client_secret":       true,
	"password":            true,
	"secret":              true,
	"assertion":           true,
	"authorization":       true,
	"cookie":              true,
	"proxy-authorization": true,
	"set-cookie":          true,
}

// OAuthErrorBodyKeys contains lower-case token endpoint error JSON keys that
// should be redacted before surfacing the response body. It intentionally does
// not include token_type.
var OAuthErrorBodyKeys = map[string]bool{
	"access_token":  true,
	"refresh_token": true,
	"id_token":      true,
	"token":         true,
	"client_secret": true,
	"password":      true,
	"assertion":     true,
}

func IsHeaderName(name string) bool {
	canonical := http.CanonicalHeaderKey(name)
	if HeaderNames[canonical] {
		return true
	}
	for _, suffix := range []string{"-Authorization", "-Cookie", "-Dpop", "-Api-Key", "-Api-Token", "-Auth-Token"} {
		if strings.HasSuffix(canonical, suffix) {
			return true
		}
	}
	return false
}

func IsHeaderValue(name, value string) bool {
	if IsHeaderName(name) {
		return true
	}
	name = http.CanonicalHeaderKey(name)
	return strings.HasSuffix(name, "-Key") && LooksSensitiveValue(value)
}

func IsQueryParamName(name string) bool {
	return QueryParamNames[strings.ToLower(name)]
}

func IsQueryParamValue(name, value string) bool {
	name = strings.ToLower(name)
	if QueryParamNames[name] {
		return true
	}
	return ambiguousQueryParamNames[name] && LooksSensitiveValue(value)
}

func IsJSONBodyKey(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if JSONBodyKeys[name] {
		return true
	}
	return strings.Contains(name, "password") || strings.Contains(name, "passphrase")
}

func IsJSONBodyValue(name, value string) bool {
	name = strings.ToLower(name)
	if JSONBodyKeys[name] || IsHeaderName(name) {
		return true
	}
	if IsJSONBodyKey(name) {
		return true
	}
	return ambiguousQueryParamNames[name] && LooksSensitiveValue(value)
}

func IsOAuthErrorBodyKey(name string) bool {
	return OAuthErrorBodyKeys[strings.ToLower(name)]
}

// RedactDiagnosticText removes common secret assignments from plugin stderr,
// verbose traces, and other diagnostic text that may be surfaced to users.
func RedactDiagnosticText(value string) string {
	value = strings.TrimSpace(value)
	for _, marker := range []string{
		"access_token",
		"refresh_token",
		"id_token",
		"client_secret",
		"api_key",
		"apikey",
		"password",
		"authorization",
		"proxy-authorization",
		"cookie",
		"dpop",
		"set-cookie",
		"x-api-key",
		"x-api-token",
		"x-auth-token",
	} {
		value = redactDiagnosticAssignment(value, marker)
	}
	return value
}

// RedactDiagnosticURLText removes credential-looking query parameters from
// URL-shaped diagnostic text, including malformed URLs that net/url cannot
// parse. It then applies the generic assignment redactor.
func RedactDiagnosticURLText(value string) string {
	value = strings.TrimSpace(value)
	searchFrom := 0
	for {
		qRel := strings.IndexByte(value[searchFrom:], '?')
		if qRel < 0 {
			return RedactDiagnosticText(value)
		}
		qStart := searchFrom + qRel
		qEnd := diagnosticQueryEnd(value, qStart+1)
		if qEnd <= qStart+1 {
			return RedactDiagnosticText(value)
		}
		query := value[qStart+1 : qEnd]
		parts := strings.Split(query, "&")
		changed := false
		kept := parts[:0]
		for _, part := range parts {
			name, rawValue, _ := strings.Cut(part, "=")
			if decoded, err := url.QueryUnescape(name); err == nil {
				name = decoded
			}
			valueForCheck := rawValue
			if decoded, err := url.QueryUnescape(rawValue); err == nil {
				valueForCheck = decoded
			}
			if IsQueryParamValue(name, valueForCheck) || invalidEscapeLooksSensitive(name, rawValue) {
				changed = true
				continue
			}
			kept = append(kept, part)
		}
		if !changed {
			searchFrom = qEnd
			continue
		}
		if len(kept) == 0 {
			value = value[:qStart] + value[qEnd:]
		} else {
			value = value[:qStart+1] + strings.Join(kept, "&") + value[qEnd:]
		}
		searchFrom = qStart
	}
}

func diagnosticQueryEnd(value string, start int) int {
	end := len(value)
	for i := start; i < len(value); i++ {
		switch value[i] {
		case '#', ' ', '\t', '\r', '\n', '"', '\'', '<', '>':
			return i
		}
	}
	return end
}

func invalidEscapeLooksSensitive(name, value string) bool {
	if _, err := url.QueryUnescape(value); err == nil {
		return false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	return ambiguousQueryParamNames[name] && len(value) >= 7
}

func redactDiagnosticAssignment(value, marker string) string {
	for _, sep := range []string{"=", ":"} {
		lower := strings.ToLower(value)
		needle := strings.ToLower(marker + sep)
		searchFrom := 0
		for {
			idxRel := strings.Index(lower[searchFrom:], needle)
			if idxRel < 0 {
				break
			}
			idx := searchFrom + idxRel
			start := idx + len(needle)
			for start < len(value) && (value[start] == ' ' || value[start] == '\t') {
				start++
			}
			end := start
			delimiters := "\r\n,;&"
			if diagnosticMarkerStopsAtSpace(marker) {
				delimiters += " \t"
			}
			for end < len(value) && !strings.ContainsRune(delimiters, rune(value[end])) {
				end++
			}
			value = value[:start] + "***" + value[end:]
			lower = strings.ToLower(value)
			searchFrom = start + len("***")
		}
	}
	return value
}

func diagnosticMarkerStopsAtSpace(marker string) bool {
	switch marker {
	case "authorization", "proxy-authorization", "cookie", "dpop", "set-cookie", "x-api-key", "x-api-token", "x-auth-token":
		return false
	default:
		return true
	}
}

// LooksSensitiveValue reports whether a value resembles an API key or token.
// It intentionally keeps common low-entropy values such as "testing" visible
// so ambiguous names like "key" can still be useful in verbose diagnostics.
func LooksSensitiveValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	switch lower {
	case "test", "testing", "example", "sample", "demo", "none", "null", "true", "false", "dev", "prod", "stage", "local", "localhost":
		return false
	}
	if strings.HasPrefix(lower, "bearer ") {
		value = strings.TrimSpace(value[len("bearer "):])
	} else if strings.HasPrefix(lower, "basic ") {
		value = strings.TrimSpace(value[len("basic "):])
	}
	if len(value) < 7 {
		return false
	}
	var hasLetter, hasDigit, hasSymbol bool
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '_' || r == '-' || r == '.' || r == '~' || r == '+' || r == '/' || r == '=':
			hasSymbol = true
		default:
			return false
		}
	}
	if hasLetter && hasDigit {
		return true
	}
	if hasSymbol && len(value) >= 10 {
		return true
	}
	return len(value) >= 20 && (hasLetter || hasDigit)
}
