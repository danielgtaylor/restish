package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/fileutil"
)

var renameTokenCacheFile = os.Rename

// CachedToken holds a cached OAuth2 access token and optional refresh token.
type CachedToken struct {
	AccessToken       string    `cbor:"access_token" json:"access_token"`
	TokenType         string    `cbor:"token_type,omitempty" json:"token_type,omitempty"`
	RefreshToken      string    `cbor:"refresh_token,omitempty" json:"refresh_token,omitempty"`
	Expiry            time.Time `cbor:"expiry,omitempty" json:"expiry,omitempty"`
	DPoPPrivateKey    []byte    `cbor:"dpop_private_key,omitempty" json:"dpop_private_key,omitempty"`
	DPoPNonce         string    `cbor:"dpop_nonce,omitempty" json:"dpop_nonce,omitempty"`
	CredentialSource  string    `cbor:"credential_source,omitempty" json:"credential_source,omitempty"`
	CredentialRef     string    `cbor:"credential_ref,omitempty" json:"credential_ref,omitempty"`
	ResourceIndicator string    `cbor:"resource_indicator,omitempty" json:"resource_indicator,omitempty"`
	Scopes            []string  `cbor:"scopes,omitempty" json:"scopes,omitempty"`
}

// IsExpired reports whether the token is expired (or will expire within 30s).
func (t *CachedToken) IsExpired() bool {
	if t.Expiry.IsZero() {
		return false
	}
	return time.Now().Add(30 * time.Second).After(t.Expiry)
}

// TokenCache persists OAuth2 tokens as a flat CBOR map at a given file path.
// All operations are safe for concurrent use.
type TokenCache struct {
	path    string
	mu      sync.Mutex
	loaded  bool
	cache   map[string]CachedToken
	modTime time.Time
	size    int64
}

// NewTokenCache returns a TokenCache that stores tokens at path.
func NewTokenCache(path string) *TokenCache {
	return &TokenCache{path: path}
}

// Path returns the file path the cache reads from and writes to.
func (c *TokenCache) Path() string {
	return c.path
}

// Get returns the cached token for key, or (nil, nil) if not found.
func (c *TokenCache) Get(key string) (*CachedToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, err := fileutil.LockSiblingFile(c.path)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	m, err := c.load()
	if err != nil {
		return nil, err
	}
	t, ok := m[key]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

// Set stores token under key, creating or updating the cache file.
func (c *TokenCache) Set(key string, token CachedToken) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, err := fileutil.LockSiblingFile(c.path)
	if err != nil {
		return err
	}
	defer lock.Close()
	m, err := c.loadLocked()
	if err != nil {
		return err
	}
	m[key] = token
	return c.saveLocked(m)
}

// Delete removes the entry for key. Returns nil when key is absent.
func (c *TokenCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, err := fileutil.LockSiblingFile(c.path)
	if err != nil {
		return err
	}
	defer lock.Close()
	m, err := c.loadLocked()
	if err != nil {
		return err
	}
	delete(m, key)
	return c.saveLocked(m)
}

// DeletePrefix removes every cached token whose key begins with prefix.
func (c *TokenCache) DeletePrefix(prefix string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, err := fileutil.LockSiblingFile(c.path)
	if err != nil {
		return err
	}
	defer lock.Close()
	m, err := c.loadLocked()
	if err != nil {
		return err
	}
	for key := range m {
		if strings.HasPrefix(key, prefix) {
			delete(m, key)
		}
	}
	return c.saveLocked(m)
}

// Refresh serializes a cached OAuth refresh for key across processes. It
// re-reads the cache under the sibling file lock, skips refresh when another
// process already stored a valid token, then stores the refreshed token before
// releasing the lock.
func (c *TokenCache) Refresh(key string, force bool, refresh func(CachedToken) (CachedToken, error)) (*CachedToken, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, err := fileutil.LockSiblingFile(c.path)
	if err != nil {
		return nil, false, err
	}
	defer lock.Close()
	m, err := c.loadLocked()
	if err != nil {
		return nil, false, err
	}
	cached, ok := m[key]
	if !ok {
		return nil, false, nil
	}
	if !force && !cached.IsExpired() {
		return &cached, false, nil
	}
	if cached.RefreshToken == "" {
		return &cached, false, nil
	}
	refreshed, err := refresh(cached)
	if err != nil {
		return &cached, false, err
	}
	m[key] = refreshed
	if err := c.saveLocked(m); err != nil {
		return nil, false, err
	}
	return &refreshed, true, nil
}

// Resolve serializes provider-backed credential acquisition for key across
// processes. The resolver receives nil when no entry exists and returns the
// complete canonical entry that should replace it.
func (c *TokenCache) Resolve(key string, resolve func(*CachedToken) (CachedToken, error)) (*CachedToken, error) {
	lock, err := fileutil.LockSiblingFile(c.resolveLockPath(key))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	current, err := c.Get(key)
	if err != nil {
		return nil, err
	}
	next, err := resolve(current)
	if saveErr := c.Set(key, next); saveErr != nil {
		return nil, saveErr
	}
	if err != nil {
		return &next, err
	}
	return &next, nil
}

func (c *TokenCache) resolveLockPath(key string) string {
	digest := sha256.Sum256([]byte(key))
	return c.path + ".resolve-" + hex.EncodeToString(digest[:])
}

func (c *TokenCache) load() (map[string]CachedToken, error) {
	// Caller must hold c.mu. This uses the cached copy when file metadata is
	// unchanged, otherwise it delegates to loadLocked for the disk read.
	if c.loaded {
		info, err := os.Stat(c.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && c.modTime.IsZero() && c.size == 0 {
				return c.cache, nil
			}
			return nil, fmt.Errorf("stat token cache %s: %w", c.path, err)
		}
		if info.ModTime().Equal(c.modTime) && info.Size() == c.size {
			return c.cache, nil
		}
	}
	return c.loadLocked()
}

func (c *TokenCache) loadLocked() (map[string]CachedToken, error) {
	// On platforms where the check is unsupported (Windows), fall through:
	// failing closed here would force a fresh browser login on every request
	// because the token written by the previous login could never be read back.
	if insecure, err := config.ConfigFileHasInsecurePermissions(c.path); err != nil && !errors.Is(err, config.ErrPermissionCheckUnsupported) {
		return nil, err
	} else if insecure {
		return nil, fmt.Errorf("token cache %s is group/world-readable; run chmod 600 %s", c.path, c.path)
	}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		c.cache = map[string]CachedToken{}
		c.loaded = true
		c.modTime = time.Time{}
		c.size = 0
		return c.cache, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]CachedToken
	if err := cbor.Unmarshal(data, &m); err != nil {
		if jsonErr := json.Unmarshal(data, &m); jsonErr != nil {
			return nil, fmt.Errorf("decoding token cache %s: cbor: %v; json: %v", c.path, err, jsonErr)
		}
	}
	if m == nil {
		m = map[string]CachedToken{}
	}
	info, statErr := os.Stat(c.path)
	if statErr == nil {
		c.modTime = info.ModTime()
		c.size = info.Size()
	}
	c.cache = m
	c.loaded = true
	return c.cache, nil
}

func (c *TokenCache) saveLocked(m map[string]CachedToken) error {
	data, err := cbor.Marshal(m)
	if err != nil {
		return err
	}
	if err := fileutil.AtomicWriteFile(c.path, data, fileutil.AtomicWriteOptions{
		FileMode:    0o600,
		DirMode:     0o700,
		TempPattern: "tokens-*.tmp",
		Rename:      renameTokenCacheFile,
	}); err != nil {
		return err
	}
	info, statErr := os.Stat(c.path)
	if statErr == nil {
		c.modTime = info.ModTime()
		c.size = info.Size()
	}
	c.cache = m
	c.loaded = true
	return nil
}

// DefaultTokenCachePath returns the path to the default token cache file,
// honoring the same RSH_CONFIG_DIR / RSH_CONFIG / XDG overrides as
// config.NewPaths. It is the canonical location for tools that want to
// share the Restish token cache.
func DefaultTokenCachePath() string {
	return config.NewPaths().TokenCache()
}

// LoadTokenCache reads the full token cache at path into a map. Returns an
// empty map when the file does not exist. External readers that do not need
// the in-process locking of TokenCache can call this directly.
func LoadTokenCache(path string) (map[string]CachedToken, error) {
	c := NewTokenCache(path)
	lock, err := fileutil.LockSiblingFile(path)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return c.loadLocked()
}

// SaveTokenCache writes m as a CBOR token cache to path, atomically and
// under the sibling file lock.
func SaveTokenCache(path string, m map[string]CachedToken) error {
	lock, err := fileutil.LockSiblingFile(path)
	if err != nil {
		return err
	}
	defer lock.Close()
	return NewTokenCache(path).saveLocked(m)
}
