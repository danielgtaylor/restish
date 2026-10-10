package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	publicauth "github.com/rest-sh/restish/v2/auth"
)

type dpopCredentialSourceStub struct {
	describe       DPoPCredentialDescription
	issue          func(string) (DPoPIssuedCredential, error)
	describeScopes *[]string
	issueScopes    *[]string
}

func (s dpopCredentialSourceStub) Describe(_ context.Context, _, _, _, _ string, scopes []string) (DPoPCredentialDescription, error) {
	if s.describeScopes != nil {
		*s.describeScopes = append([]string(nil), scopes...)
	}
	return s.describe, nil
}

func (s dpopCredentialSourceStub) Issue(_ context.Context, _, _, _, _, proof string, scopes []string) (DPoPIssuedCredential, error) {
	if s.issueScopes != nil {
		*s.issueScopes = append([]string(nil), scopes...)
	}
	return s.issue(proof)
}

func TestDPoPCredentialAcquisitionRetriesOneNonceChallengeAndRetainsNextNonce(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	description := DPoPCredentialDescription{
		ProofMethod: "POST",
		ProofURI:    "https://issuer.example.com/token",
		Resource:    "https://api.example.com",
		Scopes:      []string{"media:read"},
	}
	var proofs, describedScopes, issuedScopes []string
	source := dpopCredentialSourceStub{describe: description, describeScopes: &describedScopes, issueScopes: &issuedScopes}
	source.issue = func(proof string) (DPoPIssuedCredential, error) {
		proofs = append(proofs, proof)
		if len(proofs) == 1 {
			return DPoPIssuedCredential{}, &DPoPNonceChallenge{Nonce: "challenge-nonce"}
		}
		return DPoPIssuedCredential{
			AccessToken: "token-1", TokenType: "DPoP", ExpiresAt: now.Add(time.Minute),
			Resource: description.Resource, Scopes: description.Scopes, Nonce: "next-nonce",
		}, nil
	}
	handler := DPoP{Source: source, Now: func() time.Time { return now }}
	ac := publicauth.AuthContext{APIName: "media", ProfileName: "default", Params: map[string]string{"scopes": "media:read"}}

	token, err := handler.acquire(context.Background(), ac, nil, "realmroot", "resource-ref", []string{"media:read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 2 {
		t.Fatalf("issue attempts = %d, want 2", len(proofs))
	}
	if strings.Join(describedScopes, " ") != "media:read" || strings.Join(issuedScopes, " ") != "media:read" {
		t.Fatalf("operation scopes were not forwarded: describe=%v issue=%v", describedScopes, issuedScopes)
	}
	firstHeader, firstPayload := decodeDPoPProof(t, proofs[0])
	secondHeader, secondPayload := decodeDPoPProof(t, proofs[1])
	if _, exists := firstPayload["nonce"]; exists {
		t.Fatalf("initial proof unexpectedly contains nonce: %#v", firstPayload)
	}
	if secondPayload["nonce"] != "challenge-nonce" {
		t.Fatalf("retry nonce = %#v", secondPayload["nonce"])
	}
	if !equalJSON(firstHeader["jwk"], secondHeader["jwk"]) {
		t.Fatal("nonce retry changed the DPoP public key")
	}
	if firstPayload["jti"] == secondPayload["jti"] {
		t.Fatal("nonce retry reused the DPoP proof identifier")
	}
	if token.DPoPNonce != "next-nonce" {
		t.Fatalf("cached nonce = %q", token.DPoPNonce)
	}

	proofs = nil
	source.issue = func(proof string) (DPoPIssuedCredential, error) {
		proofs = append(proofs, proof)
		return DPoPIssuedCredential{
			AccessToken: "token-2", TokenType: "DPoP", ExpiresAt: now.Add(2 * time.Minute),
			Resource: description.Resource, Scopes: description.Scopes,
		}, nil
	}
	handler.Source = source
	renewed, err := handler.acquire(context.Background(), ac, &token, "realmroot", "resource-ref", []string{"media:read"})
	if err != nil {
		t.Fatal(err)
	}
	_, renewalPayload := decodeDPoPProof(t, proofs[0])
	if renewalPayload["nonce"] != "next-nonce" || renewed.DPoPNonce != "next-nonce" {
		t.Fatalf("renewal nonce payload=%#v cached=%q", renewalPayload["nonce"], renewed.DPoPNonce)
	}
}

func TestDPoPCredentialAcquisitionDoesNotLoopOnRepeatedNonceChallenge(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	attempts := 0
	source := dpopCredentialSourceStub{
		describe: DPoPCredentialDescription{ProofMethod: "POST", ProofURI: "https://issuer.example.com/token", Resource: "https://api.example.com"},
		issue: func(string) (DPoPIssuedCredential, error) {
			attempts++
			return DPoPIssuedCredential{}, &DPoPNonceChallenge{Nonce: "nonce"}
		},
	}
	handler := DPoP{Source: source, Now: func() time.Time { return now }}
	failed, err := handler.acquire(context.Background(), publicauth.AuthContext{}, nil, "realmroot", "resource-ref", nil)
	if err == nil || attempts != 2 {
		t.Fatalf("err=%v attempts=%d, want error after 2 attempts", err, attempts)
	}
	if failed.DPoPNonce != "nonce" {
		t.Fatalf("cached challenge nonce = %q", failed.DPoPNonce)
	}
}

func decodeDPoPProof(t *testing.T, proof string) (map[string]any, map[string]any) {
	t.Helper()
	parts := splitJWT(t, proof)
	return decodeJWTObject(t, parts[0]), decodeJWTObject(t, parts[1])
}

func splitJWT(t *testing.T, token string) []string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT parts = %d", len(parts))
	}
	return parts
}

func decodeJWTObject(t *testing.T, encoded string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func equalJSON(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}
