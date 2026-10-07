package cli

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratedCompletionCandidates(t *testing.T) {
	values := []any{"prod", "prod", "preview", "bad\n:0", strings.Repeat("x", generatedCompletionMaxValueBytes+1)}
	descriptions := []any{"Production\tWest", "duplicate", "Preview", "bad", "long"}
	got, err := generatedCompletionCandidates(values, descriptions, "pr")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prod\tProduction West", "preview\tPreview"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %#v", got)
		}
	}
}

func TestGeneratedCompletionSelector(t *testing.T) {
	doc := map[string]any{"body": map[string]any{"items": []any{map[string]any{"id": "acct-1"}}}}
	got, err := selectGeneratedCompletionValue(context.Background(), doc, "body.items.id")
	if err != nil {
		t.Fatal(err)
	}
	if got != "acct-1" {
		t.Fatalf("selector = %#v (%T)", got, got)
	}
}

func TestGeneratedCompletionHeaderSelectorIsCaseInsensitive(t *testing.T) {
	doc := map[string]any{"headers": map[string]any{"X-Request-Id": "req-1"}}
	got, err := selectGeneratedCompletionValue(context.Background(), doc, "headers.X-Request-ID")
	if err != nil || got != "req-1" {
		t.Fatalf("selector = %#v, %v", got, err)
	}
}

func TestGeneratedCompletionPreservesJSONNumbers(t *testing.T) {
	decoded, err := generatedCompletionContentRegistry().Decode("application/json", []byte(`{"id":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	value, err := selectGeneratedCompletionValue(context.Background(), map[string]any{"body": decoded}, "body.id")
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := generatedCompletionCandidates(value, nil, "")
	if err != nil || len(candidates) != 1 || candidates[0] != "9007199254740993" {
		t.Fatalf("candidates = %#v, %v", candidates, err)
	}
}

func TestGeneratedCompletionCandidatesRequireMatchingDescriptions(t *testing.T) {
	if _, err := generatedCompletionCandidates([]any{"one", "two"}, []any{"One"}, ""); err == nil {
		t.Fatal("mismatched descriptions accepted")
	}
}

func TestGeneratedCompletionCandidatesFilterBeforeLimit(t *testing.T) {
	values := make([]any, generatedCompletionMaxCandidates+1)
	for i := range values {
		values[i] = "other"
	}
	values[len(values)-1] = "prod"
	got, err := generatedCompletionCandidates(values, nil, "prod")
	if err != nil || len(got) != 1 || got[0] != "prod" {
		t.Fatalf("candidates = %#v, %v", got, err)
	}
}

func TestGeneratedCompletionRejectsShellControlValues(t *testing.T) {
	for _, value := range []string{"_activeHelp_ injected", " leading", "trailing "} {
		if safeCompletionValue(value) {
			t.Fatalf("unsafe completion value %q accepted", value)
		}
	}
}
