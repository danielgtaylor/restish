package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	publicauth "github.com/rest-sh/restish/v2/auth"
)

// DPoPCredentialDescription is the public acquisition metadata supplied by a
// provider-specific source before Restish creates the token-endpoint proof.
type DPoPCredentialDescription struct {
	ProofMethod string
	ProofURI    string
	Resource    string
	Scopes      []string
}

// DPoPIssuedCredential is the short-lived credential returned after the
// source verifies Restish's proof.
type DPoPIssuedCredential struct {
	AccessToken string
	TokenType   string
	ExpiresAt   time.Time
	Resource    string
	Scopes      []string
	Nonce       string
}

// DPoPNonceChallenge is returned by a credential source when its authorization
// server requires a fresh proof containing the supplied opaque nonce.
type DPoPNonceChallenge struct {
	Nonce string
}

func (e *DPoPNonceChallenge) Error() string { return "authorization server requires a DPoP nonce" }

// DPoPCredentialSource is the inward port used by the native DPoP handler.
// Provider adapters never receive or persist the target private key.
type DPoPCredentialSource interface {
	Describe(ctx context.Context, apiName, profileName, sourceName, reference string, scopes []string) (DPoPCredentialDescription, error)
	Issue(ctx context.Context, apiName, profileName, sourceName, reference, proof string, scopes []string) (DPoPIssuedCredential, error)
}

type resolvingTokenStore interface {
	publicauth.TokenStore
	Resolve(string, func(*publicauth.CachedToken) (publicauth.CachedToken, error)) (*publicauth.CachedToken, error)
}

// DPoP owns an RFC 9449 key, proof generation, and provider-backed token
// lifecycle for one configured credential binding.
type DPoP struct {
	Source DPoPCredentialSource
	Now    func() time.Time
	Random io.Reader
}

type dpopNonceContextKey struct{}

// SetDPoPNonce attaches a Resource Server nonce to the retry request without
// exposing it as an outbound implementation header.
func SetDPoPNonce(req *http.Request, nonce string) {
	if req == nil || nonce == "" {
		return
	}
	*req = *req.WithContext(context.WithValue(req.Context(), dpopNonceContextKey{}, nonce))
}

func (h *DPoP) Parameters() []publicauth.Param {
	return []publicauth.Param{
		{Name: "source", Description: "Credential-source plugin name", Required: true},
		{Name: "reference", Description: "Opaque credential-source reference", Required: true},
	}
}

func (h *DPoP) SupportsForce() {}

func (h *DPoP) SupportsRequestBinding() {}

func (h *DPoP) Authenticate(ctx context.Context, req *http.Request, ac publicauth.AuthContext) error {
	if h.Source == nil {
		return errors.New("dpop: credential source is unavailable")
	}
	store, ok := ac.TokenStore.(resolvingTokenStore)
	if !ok {
		return errors.New("dpop: token store does not support serialized credential acquisition")
	}
	if ac.CacheKey == "" {
		return errors.New("dpop: cache key is required")
	}
	sourceName := strings.TrimSpace(ac.Params["source"])
	reference := strings.TrimSpace(ac.Params["reference"])
	if sourceName == "" || reference == "" {
		return errors.New("dpop: source and reference are required")
	}
	requiredScopes := splitScopes(ac.Params["scopes"])
	nonce, _ := req.Context().Value(dpopNonceContextKey{}).(string)
	token, err := store.Resolve(ac.CacheKey, func(current *publicauth.CachedToken) (publicauth.CachedToken, error) {
		if (!ac.Force || nonce != "") && reusableDPoPToken(current, sourceName, reference, requiredScopes, h.now()) {
			return *current, nil
		}
		return h.acquire(ctx, ac, current, sourceName, reference, requiredScopes)
	})
	if err != nil {
		return err
	}
	privateKey, err := parseDPoPPrivateKey(token.DPoPPrivateKey)
	if err != nil {
		return fmt.Errorf("dpop: load cached private key: %w", err)
	}
	if !dpopResourceCoversRequest(token.ResourceIndicator, req.URL) {
		return fmt.Errorf("dpop: credential Resource %q does not cover request target %q", token.ResourceIndicator, req.URL.String())
	}
	proof, err := h.signProof(privateKey, req.Method, req.URL.String(), token.AccessToken, nonce)
	if err != nil {
		return fmt.Errorf("dpop: sign protected-resource proof: %w", err)
	}
	req.Header.Set("Authorization", "DPoP "+token.AccessToken)
	req.Header.Set("DPoP", proof)
	return nil
}

func (h *DPoP) acquire(
	ctx context.Context,
	ac publicauth.AuthContext,
	current *publicauth.CachedToken,
	sourceName string,
	reference string,
	requiredScopes []string,
) (publicauth.CachedToken, error) {
	privateKey, encodedKey, err := h.resolvePrivateKey(current, sourceName, reference)
	if err != nil {
		return publicauth.CachedToken{}, fmt.Errorf("dpop: prepare private key: %w", err)
	}
	base := publicauth.CachedToken{
		TokenType:        "DPoP",
		DPoPPrivateKey:   encodedKey,
		CredentialSource: sourceName,
		CredentialRef:    reference,
	}
	nonce := reusableCredentialNonce(current, sourceName, reference)
	base.DPoPNonce = nonce
	description, err := h.Source.Describe(ctx, ac.APIName, ac.ProfileName, sourceName, reference, requiredScopes)
	if err != nil {
		return base, fmt.Errorf("dpop credential source %q describe: %w", sourceName, err)
	}
	if err := validateDPoPDescription(description, requiredScopes); err != nil {
		return base, fmt.Errorf("dpop credential source %q: %w", sourceName, err)
	}
	base.ResourceIndicator = description.Resource
	base.Scopes = append([]string(nil), description.Scopes...)
	proof, err := h.signProof(privateKey, description.ProofMethod, description.ProofURI, "", nonce)
	if err != nil {
		return base, fmt.Errorf("dpop: sign credential proof: %w", err)
	}
	issued, err := h.Source.Issue(ctx, ac.APIName, ac.ProfileName, sourceName, reference, proof, requiredScopes)
	if err != nil {
		var challenge *DPoPNonceChallenge
		if !errors.As(err, &challenge) {
			return base, fmt.Errorf("dpop credential source %q issue: %w", sourceName, err)
		}
		if err := validateDPoPNonce(challenge.Nonce); err != nil {
			return base, fmt.Errorf("dpop credential source %q returned an invalid challenge: %w", sourceName, err)
		}
		nonce = challenge.Nonce
		base.DPoPNonce = nonce
		proof, err = h.signProof(privateKey, description.ProofMethod, description.ProofURI, "", nonce)
		if err != nil {
			return base, fmt.Errorf("dpop: sign nonce-bound credential proof: %w", err)
		}
		issued, err = h.Source.Issue(ctx, ac.APIName, ac.ProfileName, sourceName, reference, proof, requiredScopes)
		if err != nil {
			var nextChallenge *DPoPNonceChallenge
			if errors.As(err, &nextChallenge) && validateDPoPNonce(nextChallenge.Nonce) == nil {
				base.DPoPNonce = nextChallenge.Nonce
			}
			return base, fmt.Errorf("dpop credential source %q issue after nonce challenge: %w", sourceName, err)
		}
	}
	if err := validateIssuedDPoPCredential(issued, description, requiredScopes, h.now()); err != nil {
		return base, fmt.Errorf("dpop credential source %q: %w", sourceName, err)
	}
	if issued.Nonce != "" {
		if err := validateDPoPNonce(issued.Nonce); err != nil {
			return base, fmt.Errorf("dpop credential source %q returned an invalid next nonce: %w", sourceName, err)
		}
		nonce = issued.Nonce
	}
	return publicauth.CachedToken{
		AccessToken:       issued.AccessToken,
		TokenType:         issued.TokenType,
		Expiry:            issued.ExpiresAt,
		DPoPPrivateKey:    encodedKey,
		DPoPNonce:         nonce,
		CredentialSource:  sourceName,
		CredentialRef:     reference,
		ResourceIndicator: issued.Resource,
		Scopes:            append([]string(nil), issued.Scopes...),
	}, nil
}

func reusableCredentialNonce(current *publicauth.CachedToken, sourceName, reference string) string {
	if current == nil || current.CredentialSource != sourceName || current.CredentialRef != reference || len(current.DPoPPrivateKey) == 0 {
		return ""
	}
	return current.DPoPNonce
}

func validateDPoPNonce(value string) error {
	if value == "" || len(value) > 4096 {
		return errors.New("nonce must contain between 1 and 4096 characters")
	}
	for _, char := range []byte(value) {
		if char != 0x21 && (char < 0x23 || char > 0x5b) && (char < 0x5d || char > 0x7e) {
			return errors.New("nonce contains a character outside the RFC 9449 syntax")
		}
	}
	return nil
}

func (h *DPoP) resolvePrivateKey(current *publicauth.CachedToken, sourceName, reference string) (*ecdsa.PrivateKey, []byte, error) {
	if current != nil && current.CredentialSource == sourceName && current.CredentialRef == reference && len(current.DPoPPrivateKey) > 0 {
		key, err := parseDPoPPrivateKey(current.DPoPPrivateKey)
		return key, append([]byte(nil), current.DPoPPrivateKey...), err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), h.random())
	if err != nil {
		return nil, nil, err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return key, encoded, nil
}

func (h *DPoP) signProof(key *ecdsa.PrivateKey, method, rawURI, accessToken, nonce string) (string, error) {
	htu, err := normalizedDPoPURI(rawURI)
	if err != nil {
		return "", err
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return "", errors.New("proof method is required")
	}
	jtiBytes := make([]byte, 16)
	if _, err := io.ReadFull(h.random(), jtiBytes); err != nil {
		return "", err
	}
	size := (key.Curve.Params().BitSize + 7) / 8
	header := map[string]any{
		"typ": "dpop+jwt",
		"alg": "ES256",
		"jwk": map[string]string{
			"kty": "EC",
			"crv": "P-256",
			"x":   base64.RawURLEncoding.EncodeToString(key.PublicKey.X.FillBytes(make([]byte, size))),
			"y":   base64.RawURLEncoding.EncodeToString(key.PublicKey.Y.FillBytes(make([]byte, size))),
		},
	}
	payload := map[string]any{
		"jti": base64.RawURLEncoding.EncodeToString(jtiBytes),
		"htm": method,
		"htu": htu,
		"iat": h.now().Unix(),
	}
	if accessToken != "" {
		digest := sha256.Sum256([]byte(accessToken))
		payload["ath"] = base64.RawURLEncoding.EncodeToString(digest[:])
	}
	if nonce != "" {
		payload["nonce"] = nonce
	}
	encodedHeader, err := encodeJWTPart(header)
	if err != nil {
		return "", err
	}
	encodedPayload, err := encodeJWTPart(payload)
	if err != nil {
		return "", err
	}
	signingInput := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(h.random(), key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, size*2)
	r.FillBytes(signature[:size])
	s.FillBytes(signature[size:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func encodeJWTPart(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func parseDPoPPrivateKey(encoded []byte) (*ecdsa.PrivateKey, error) {
	parsed, err := x509.ParsePKCS8PrivateKey(encoded)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("cached key is not an ES256 private key")
	}
	return key, nil
}

func normalizedDPoPURI(rawURI string) (string, error) {
	u, err := url.Parse(rawURI)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", fmt.Errorf("proof URI %q must be absolute", rawURI)
	}
	if u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("proof URI %q must not contain user information or a fragment", rawURI)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackDPoPHost(u.Hostname())) {
		return "", fmt.Errorf("proof URI %q must use HTTPS unless it is loopback HTTP", rawURI)
	}
	u.RawQuery = ""
	u.ForceQuery = false
	return u.String(), nil
}

func isLoopbackDPoPHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func dpopResourceCoversRequest(resource string, target *url.URL) bool {
	resourceURL, err := url.Parse(resource)
	if err != nil || target == nil || !sameDPoPOrigin(resourceURL, target) {
		return false
	}
	resourcePath := path.Clean("/" + strings.TrimPrefix(resourceURL.Path, "/"))
	targetPath := path.Clean("/" + strings.TrimPrefix(target.Path, "/"))
	if resourcePath == "/" {
		return true
	}
	return targetPath == resourcePath || strings.HasPrefix(targetPath, resourcePath+"/")
}

func sameDPoPOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) && effectiveDPoPPort(left) == effectiveDPoPPort(right)
}

func effectiveDPoPPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	if strings.EqualFold(value.Scheme, "http") {
		return "80"
	}
	return ""
}

func validateDPoPDescription(description DPoPCredentialDescription, requiredScopes []string) error {
	if _, err := normalizedDPoPURI(description.ProofURI); err != nil {
		return err
	}
	if strings.TrimSpace(description.ProofMethod) == "" {
		return errors.New("credential proof method is required")
	}
	if _, err := normalizedDPoPURI(description.Resource); err != nil {
		return fmt.Errorf("invalid Resource indicator: %w", err)
	}
	if !scopeSetContains(description.Scopes, requiredScopes) {
		return errors.New("credential offer does not cover the operation scopes")
	}
	return nil
}

func validateIssuedDPoPCredential(issued DPoPIssuedCredential, description DPoPCredentialDescription, requiredScopes []string, now time.Time) error {
	if !strings.EqualFold(issued.TokenType, "DPoP") || strings.TrimSpace(issued.AccessToken) == "" {
		return errors.New("issued credential is not a DPoP access token")
	}
	if !issued.ExpiresAt.After(now) {
		return errors.New("issued credential is already expired")
	}
	if issued.Resource != description.Resource {
		return errors.New("issued credential Resource indicator differs from its offer")
	}
	if !scopeSetContains(issued.Scopes, description.Scopes) || !scopeSetContains(issued.Scopes, requiredScopes) {
		return errors.New("issued credential does not cover the offered scopes")
	}
	return nil
}

func reusableDPoPToken(token *publicauth.CachedToken, sourceName, reference string, requiredScopes []string, now time.Time) bool {
	return token != nil && token.AccessToken != "" && strings.EqualFold(token.TokenType, "DPoP") &&
		token.CredentialSource == sourceName && token.CredentialRef == reference && len(token.DPoPPrivateKey) > 0 &&
		now.Add(30*time.Second).Before(token.Expiry) && scopeSetContains(token.Scopes, requiredScopes)
}

func splitScopes(raw string) []string {
	values := strings.Fields(raw)
	sort.Strings(values)
	return slicesCompact(values)
}

func slicesCompact(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func scopeSetContains(have, need []string) bool {
	set := make(map[string]bool, len(have))
	for _, value := range have {
		set[value] = true
	}
	for _, value := range need {
		if !set[value] {
			return false
		}
	}
	return true
}

func (h *DPoP) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *DPoP) random() io.Reader {
	if h.Random != nil {
		return h.Random
	}
	return rand.Reader
}
