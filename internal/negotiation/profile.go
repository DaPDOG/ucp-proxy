package negotiation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProfileFetcher fetches and caches agent profiles.
// Interface allows mocking in tests.
type ProfileFetcher interface {
	Fetch(ctx context.Context, profileURL string) (*AgentProfile, error)
}

// DefaultCacheTTL is used when HTTP cache headers don't specify a duration.
const DefaultCacheTTL = 5 * time.Minute

// DefaultFetchTimeout is the timeout for fetching a profile.
const DefaultFetchTimeout = 5 * time.Second

// MaxCacheEntries limits the number of cached profiles (LRU eviction).
const MaxCacheEntries = 1000

// ProfileFetcherConfig contains configuration for the profile fetcher.
type ProfileFetcherConfig struct {
	CacheTTL     time.Duration // Default TTL when not specified by cache headers
	FetchTimeout time.Duration // HTTP timeout for fetching profiles
	MaxEntries   int           // Max cache entries (0 = default)
}

// HTTPProfileFetcher fetches agent profiles over HTTP with caching.
// Respects HTTP Cache-Control headers per UCP spec Section 5.6.
type HTTPProfileFetcher struct {
	client     *http.Client
	cache      map[string]*cacheEntry
	cacheMu    sync.RWMutex
	config     ProfileFetcherConfig
	accessList []string // LRU tracking: most recent at end
}

type cacheEntry struct {
	profile   *AgentProfile
	expiresAt time.Time
	etag      string
}

// NewHTTPProfileFetcher creates a new profile fetcher with default config.
func NewHTTPProfileFetcher() *HTTPProfileFetcher {
	return NewHTTPProfileFetcherWithConfig(ProfileFetcherConfig{
		CacheTTL:     DefaultCacheTTL,
		FetchTimeout: DefaultFetchTimeout,
		MaxEntries:   MaxCacheEntries,
	})
}

// NewHTTPProfileFetcherWithConfig creates a profile fetcher with custom config.
func NewHTTPProfileFetcherWithConfig(config ProfileFetcherConfig) *HTTPProfileFetcher {
	if config.CacheTTL == 0 {
		config.CacheTTL = DefaultCacheTTL
	}
	if config.FetchTimeout == 0 {
		config.FetchTimeout = DefaultFetchTimeout
	}
	if config.MaxEntries == 0 {
		config.MaxEntries = MaxCacheEntries
	}

	return &HTTPProfileFetcher{
		client: &http.Client{
			Timeout: config.FetchTimeout,
		},
		cache:      make(map[string]*cacheEntry),
		config:     config,
		accessList: make([]string, 0, config.MaxEntries),
	}
}

// Fetch retrieves an agent profile, using cache when possible.
// If cached entry is fresh, returns it immediately.
// If cached entry is stale, attempts revalidation with ETag.
// On fetch failure with stale cache, returns stale data (best effort).
func (f *HTTPProfileFetcher) Fetch(ctx context.Context, profileURL string) (*AgentProfile, error) {
	// Check cache first
	f.cacheMu.RLock()
	entry, exists := f.cache[profileURL]
	f.cacheMu.RUnlock()

	now := time.Now()

	// Fresh cache hit - return immediately
	if exists && entry.expiresAt.After(now) {
		f.recordAccess(profileURL)
		return entry.profile, nil
	}

	// Fetch (possibly with revalidation)
	profile, err := f.fetchFromNetwork(ctx, profileURL, entry)
	if err != nil {
		// Fetch failed - use stale cache if available (best effort per PRD)
		if exists {
			return entry.profile, nil
		}
		return nil, fmt.Errorf("fetch agent profile: %w", err)
	}

	return profile, nil
}

func (f *HTTPProfileFetcher) fetchFromNetwork(ctx context.Context, profileURL string, staleEntry *cacheEntry) (*AgentProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set Accept header per UCP spec
	req.Header.Set("Accept", "application/json")

	// Conditional request if we have an ETag from previous fetch
	if staleEntry != nil && staleEntry.etag != "" {
		req.Header.Set("If-None-Match", staleEntry.etag)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// 304 Not Modified - cache is still valid
	if resp.StatusCode == http.StatusNotModified && staleEntry != nil {
		// Refresh TTL and return cached entry
		f.updateCacheEntry(profileURL, staleEntry.profile, resp)
		return staleEntry.profile, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, profileURL)
	}

	// Read and parse body (limit to 1MB to prevent abuse)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var profile AgentProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("parse profile JSON: %w", err)
	}

	profile.ProfileURL = profileURL
	profile.FetchedAt = time.Now()

	// Cache the result
	f.updateCacheEntry(profileURL, &profile, resp)

	return &profile, nil
}

func (f *HTTPProfileFetcher) updateCacheEntry(url string, profile *AgentProfile, resp *http.Response) {
	ttl := f.parseCacheTTL(resp)
	expiresAt := time.Now().Add(ttl)
	profile.ExpiresAt = expiresAt

	entry := &cacheEntry{
		profile:   profile,
		expiresAt: expiresAt,
		etag:      resp.Header.Get("ETag"),
	}

	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()

	// LRU eviction if at capacity
	if len(f.cache) >= f.config.MaxEntries {
		f.evictOldest()
	}

	f.cache[url] = entry
	f.recordAccessLocked(url)
}

// parseCacheTTL extracts TTL from HTTP cache headers.
// Priority: max-age in Cache-Control, then Expires header, then default.
func (f *HTTPProfileFetcher) parseCacheTTL(resp *http.Response) time.Duration {
	// Check Cache-Control: max-age=N
	cc := resp.Header.Get("Cache-Control")
	if cc != "" {
		for _, directive := range strings.Split(cc, ",") {
			directive = strings.TrimSpace(directive)
			if strings.HasPrefix(directive, "max-age=") {
				if seconds, err := strconv.Atoi(strings.TrimPrefix(directive, "max-age=")); err == nil && seconds >= 0 {
					return time.Duration(seconds) * time.Second
				}
			}
		}
	}

	// Check Expires header
	if expires := resp.Header.Get("Expires"); expires != "" {
		if t, err := http.ParseTime(expires); err == nil {
			if ttl := time.Until(t); ttl > 0 {
				return ttl
			}
		}
	}

	// Fallback to default
	return f.config.CacheTTL
}

func (f *HTTPProfileFetcher) recordAccess(url string) {
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	f.recordAccessLocked(url)
}

func (f *HTTPProfileFetcher) recordAccessLocked(url string) {
	// Remove existing occurrence
	for i, u := range f.accessList {
		if u == url {
			f.accessList = append(f.accessList[:i], f.accessList[i+1:]...)
			break
		}
	}
	// Add to end (most recent)
	f.accessList = append(f.accessList, url)
}

func (f *HTTPProfileFetcher) evictOldest() {
	if len(f.accessList) == 0 {
		return
	}
	oldest := f.accessList[0]
	f.accessList = f.accessList[1:]
	delete(f.cache, oldest)
}

// ClearCache removes all cached entries. Useful for testing.
func (f *HTTPProfileFetcher) ClearCache() {
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	f.cache = make(map[string]*cacheEntry)
	f.accessList = make([]string, 0, f.config.MaxEntries)
}
