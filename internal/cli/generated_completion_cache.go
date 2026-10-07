package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	diskcache "github.com/rest-sh/restish/v2/internal/cache"
	"github.com/rest-sh/restish/v2/internal/spec"
)

const (
	generatedCompletionCacheTTL      = 30 * time.Second
	generatedCompletionCacheMaxBytes = 1 << 20
)

type generatedCompletionCacheEntry struct {
	ExpiresAt  time.Time `json:"expires_at"`
	Candidates []string  `json:"candidates"`
}

func (c *CLI) loadGeneratedCompletionCache(apiName, profileName, key string) ([]string, bool) {
	cache, err := c.generatedCompletionDiskCache(apiName, profileName)
	if err != nil {
		return nil, false
	}
	storageKey := "https://completion.invalid/" + generatedCompletionCacheKey(key)
	data, ok := cache.Get(storageKey)
	if !ok || len(data) > generatedCompletionCacheMaxBytes {
		return nil, false
	}
	var entry generatedCompletionCacheEntry
	if json.Unmarshal(data, &entry) != nil || time.Now().After(entry.ExpiresAt) || len(entry.Candidates) > generatedCompletionMaxCandidates {
		cache.Delete(storageKey)
		return nil, false
	}
	candidates := make([]string, 0, len(entry.Candidates))
	for _, candidate := range entry.Candidates {
		value, description, hasDescription := strings.Cut(candidate, "\t")
		if !safeCompletionValue(value) {
			return nil, false
		}
		if hasDescription {
			candidate = value + "\t" + sanitizeCompletionDescription(description)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, true
}

func (c *CLI) storeGeneratedCompletionCache(apiName, profileName, key string, candidates []string) {
	cache, err := c.generatedCompletionDiskCache(apiName, profileName)
	if err != nil {
		return
	}
	data, err := json.Marshal(generatedCompletionCacheEntry{
		ExpiresAt: time.Now().Add(generatedCompletionCacheTTL), Candidates: candidates,
	})
	if err == nil && len(data) <= generatedCompletionCacheMaxBytes {
		cache.Set("https://completion.invalid/"+generatedCompletionCacheKey(key), data)
	}
}

func (c *CLI) generatedCompletionDiskCache(apiName, profileName string) (*diskcache.DiskCache, error) {
	maxBytes, err := c.cacheMaxBytes()
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = diskcache.DefaultMaxBytes
	}
	return diskcache.New(c.cacheDir(), maxBytes, c.apiCacheNamespace(apiName, profileName))
}

func generatedCompletionCacheKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)
}

func (c *CLI) generatedCompletionConfigKey(apiName string, flags GlobalFlags) string {
	var api any
	var authProfiles any
	if c.cfg != nil && c.cfg.APIs != nil {
		api = c.cfg.APIs[apiName]
		authProfiles = c.cfg.AuthProfiles
	}
	data, _ := json.Marshal(struct {
		API          any
		AuthProfiles any
		Flags        GlobalFlags
	}{api, authProfiles, flags})
	return generatedCompletionCacheKey(string(data))
}

func generatedCompletionProviderKey(provider spec.Operation) string {
	data, _ := json.Marshal(struct {
		NoAuth                 bool
		OptionalAuth           bool
		CredentialAlternatives []spec.CredentialAlternative
	}{provider.NoAuth, provider.OptionalAuth, provider.CredentialAlternatives})
	return generatedCompletionCacheKey(string(data))
}
