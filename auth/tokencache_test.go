package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
)

func TestTokenCache_MissingFile(t *testing.T) {
	tc := NewTokenCache(filepath.Join(t.TempDir(), "tokens.json"))
	got, err := tc.Get("key")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil token for missing file, got %+v", got)
	}
}

func TestTokenCache_Path(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	tc := NewTokenCache(path)
	if got := tc.Path(); got != path {
		t.Errorf("Path() = %q, want %q", got, path)
	}
}

func TestTokenCache_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	tc := NewTokenCache(path)
	tok := CachedToken{
		AccessToken:  "abc123",
		TokenType:    "bearer",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(time.Hour).Truncate(time.Second),
	}
	if err := tc.Set("mykey", tok); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := tc.Get("mykey")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected token, got nil")
	}
	if got.AccessToken != tok.AccessToken {
		t.Errorf("AccessToken: got %q, want %q", got.AccessToken, tok.AccessToken)
	}
	if got.RefreshToken != tok.RefreshToken {
		t.Errorf("RefreshToken: got %q, want %q", got.RefreshToken, tok.RefreshToken)
	}
}

func TestTokenCache_WritesCBOR(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	tc := NewTokenCache(path)
	if err := tc.Set("mykey", CachedToken{AccessToken: "abc123"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var decoded map[string]CachedToken
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("expected CBOR token cache: %v", err)
	}
	if decoded["mykey"].AccessToken != "abc123" {
		t.Fatalf("decoded token = %+v", decoded["mykey"])
	}
	if json.Valid(data) {
		t.Fatalf("token cache should not be JSON: %q", data)
	}
}

func TestTokenCache_ReadsLegacyJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	if err := os.WriteFile(path, []byte(`{"mykey":{"access_token":"legacy"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tc := NewTokenCache(path)
	got, err := tc.Get("mykey")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.AccessToken != "legacy" {
		t.Fatalf("expected legacy JSON token, got %+v", got)
	}
}

func TestTokenCache_NullFileIsTreatedAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tc := NewTokenCache(path)
	got, err := tc.Get("missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil token, got %+v", got)
	}
	if err := tc.Set("key", CachedToken{AccessToken: "new"}); err != nil {
		t.Fatalf("Set after null cache: %v", err)
	}
	got, err = tc.Get("key")
	if err != nil {
		t.Fatalf("Get new key: %v", err)
	}
	if got == nil || got.AccessToken != "new" {
		t.Fatalf("expected stored token after null cache, got %+v", got)
	}
}

func TestTokenCache_EmptyObjectFileIsTreatedAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tc := NewTokenCache(path)
	got, err := tc.Get("missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil token, got %+v", got)
	}
	if err := tc.Set("key", CachedToken{AccessToken: "new"}); err != nil {
		t.Fatalf("Set after empty object cache: %v", err)
	}
}

func TestTokenCache_ReloadsWhenSizeChangesWithSameModTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	tc := NewTokenCache(path)
	if err := tc.Set("mykey", CachedToken{AccessToken: "first"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := tc.Get("mykey"); err != nil {
		t.Fatalf("initial Get: %v", err)
	}
	originalMtime := tc.modTime

	updated := map[string]CachedToken{
		"mykey": {AccessToken: "second-token-that-changes-size"},
	}
	data, err := cbor.Marshal(updated)
	if err != nil {
		t.Fatalf("marshal updated cache: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("overwrite cache: %v", err)
	}
	if err := os.Chtimes(path, originalMtime, originalMtime); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}

	got, err := tc.Get("mykey")
	if err != nil {
		t.Fatalf("Get after same-mtime overwrite: %v", err)
	}
	if got == nil || got.AccessToken != "second-token-that-changes-size" {
		t.Fatalf("expected reloaded token, got %+v", got)
	}
}

func TestTokenCache_SaveCleansTempFileOnRenameError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.cbor")
	oldRename := renameTokenCacheFile
	renameTokenCacheFile = func(oldpath, newpath string) error {
		return errors.New("rename failed")
	}
	t.Cleanup(func() { renameTokenCacheFile = oldRename })

	tc := NewTokenCache(path)
	err := tc.Set("mykey", CachedToken{AccessToken: "abc"})
	if err == nil {
		t.Fatal("expected rename error")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "tokens-*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no orphan temp files, got %v", matches)
	}
}

func TestTokenCache_ArrayFileReturnsDecodeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	if err := os.WriteFile(path, []byte("[]"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tc := NewTokenCache(path)
	_, err := tc.Get("missing")
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestTokenCache_Delete(t *testing.T) {
	tc := NewTokenCache(filepath.Join(t.TempDir(), "tokens.json"))
	_ = tc.Set("key", CachedToken{AccessToken: "token"})
	if err := tc.Delete("key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := tc.Get("key")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestTokenCache_Delete_Missing(t *testing.T) {
	tc := NewTokenCache(filepath.Join(t.TempDir(), "tokens.json"))
	if err := tc.Delete("no-such-key"); err != nil {
		t.Errorf("Delete of absent key should return nil, got %v", err)
	}
}

func TestTokenCache_FilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	tc := NewTokenCache(path)
	if err := tc.Set("k", CachedToken{AccessToken: "x"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected file permission 0600, got %04o", perm)
	}
}

func TestTokenCache_RejectsInsecurePermissionsOnRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not authoritative on Windows")
	}
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	if err := os.WriteFile(path, []byte(`{"mykey":{"access_token":"legacy"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tc := NewTokenCache(path)
	_, err := tc.Get("mykey")
	if err == nil {
		t.Fatal("expected insecure permission error")
	}
}

func TestTokenCache_DirPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(dir, "tokens.json")
	tc := NewTokenCache(path)
	if err := tc.Set("k", CachedToken{AccessToken: "x"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("expected dir permission 0700, got %04o", perm)
	}
}

func TestCachedToken_IsExpired(t *testing.T) {
	past := CachedToken{Expiry: time.Now().Add(-time.Hour)}
	if !past.IsExpired() {
		t.Error("expected expired token to be IsExpired")
	}
	future := CachedToken{Expiry: time.Now().Add(time.Hour)}
	if future.IsExpired() {
		t.Error("expected unexpired token to not be IsExpired")
	}
	zero := CachedToken{}
	if zero.IsExpired() {
		t.Error("token with zero Expiry should not be IsExpired")
	}
}

func TestTokenCache_ConcurrentSetPreservesBothEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	tc1 := NewTokenCache(path)
	tc2 := NewTokenCache(path)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := tc1.Set("api:default", CachedToken{AccessToken: "tok1"}); err != nil {
			t.Errorf("tc1.Set: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := tc2.Set("api:prod", CachedToken{AccessToken: "tok2"}); err != nil {
			t.Errorf("tc2.Set: %v", err)
		}
	}()
	wg.Wait()

	tc3 := NewTokenCache(path)
	for key := range map[string]string{"api:default": "tok1", "api:prod": "tok2"} {
		got, err := tc3.Get(key)
		if err != nil {
			t.Fatalf("Get(%q): %v", key, err)
		}
		if got == nil {
			t.Fatalf("expected token for %q", key)
		}
	}
}

func TestTokenCache_ReloadsOnExternalChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	tc := NewTokenCache(path)
	if err := tc.Set("key", CachedToken{AccessToken: "old"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := os.WriteFile(path, []byte(`{"key":{"access_token":"new"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mtime := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	got, err := tc.Get("key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.AccessToken != "new" {
		t.Fatalf("expected reloaded token, got %+v", got)
	}
}

func TestTokenCache_ConcurrentRefreshReusesNewToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	tc1 := NewTokenCache(path)
	tc2 := NewTokenCache(path)
	if err := tc1.Set("api:default", CachedToken{
		AccessToken:  "old",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var refreshCalls atomic.Int32
	refresh := func(CachedToken) (CachedToken, error) {
		if refreshCalls.Add(1) == 1 {
			close(started)
			<-release
		}
		return CachedToken{
			AccessToken:  "new",
			RefreshToken: "rotated",
			Expiry:       time.Now().Add(time.Hour),
		}, nil
	}

	errCh := make(chan error, 2)
	tokenCh := make(chan *CachedToken, 2)
	go func() {
		token, _, err := tc1.Refresh("api:default", false, refresh)
		tokenCh <- token
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not start")
	}
	go func() {
		token, _, err := tc2.Refresh("api:default", false, refresh)
		tokenCh <- token
		errCh <- err
	}()
	close(release)

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		token := <-tokenCh
		if token == nil || token.AccessToken != "new" || token.RefreshToken != "rotated" {
			t.Fatalf("token = %+v, want refreshed token", token)
		}
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

func TestTokenCache_ResolveDoesNotBlockOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	pending := NewTokenCache(path)
	other := NewTokenCache(path)
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	pendingDone := make(chan error, 1)
	go func() {
		_, err := pending.Resolve("pending", func(*CachedToken) (CachedToken, error) {
			close(started)
			<-release
			return CachedToken{AccessToken: "approved"}, nil
		})
		pendingDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("pending resolver did not start")
	}

	otherDone := make(chan error, 1)
	go func() {
		_, err := other.Resolve("other", func(*CachedToken) (CachedToken, error) {
			return CachedToken{AccessToken: "independent"}, nil
		})
		otherDone <- err
	}()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatalf("Resolve(other): %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Resolve(other) blocked behind an unrelated pending acquisition")
	}

	close(release)
	if err := <-pendingDone; err != nil {
		t.Fatalf("Resolve(pending): %v", err)
	}
	for key, want := range map[string]string{"pending": "approved", "other": "independent"} {
		got, err := NewTokenCache(path).Get(key)
		if err != nil {
			t.Fatalf("Get(%q): %v", key, err)
		}
		if got == nil || got.AccessToken != want {
			t.Fatalf("Get(%q) = %+v, want access token %q", key, got, want)
		}
	}
}

func TestTokenCache_ConcurrentResolveSerializesSameKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.cbor")
	first := NewTokenCache(path)
	second := NewTokenCache(path)
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var acquisitions atomic.Int32
	resolve := func(current *CachedToken) (CachedToken, error) {
		if current != nil {
			return *current, nil
		}
		if acquisitions.Add(1) == 1 {
			close(started)
			<-release
		}
		return CachedToken{AccessToken: "resolved"}, nil
	}

	errCh := make(chan error, 2)
	go func() {
		_, err := first.Resolve("shared", resolve)
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first resolver did not start")
	}
	go func() {
		_, err := second.Resolve("shared", resolve)
		errCh <- err
	}()
	close(release)

	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatalf("Resolve(shared): %v", err)
		}
	}
	if got := acquisitions.Load(); got != 1 {
		t.Fatalf("acquisitions = %d, want 1", got)
	}
}
