package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubPagedRedis is a thread-safe RedisClient fake with scripted SCAN pages.
// MockRedisClient is not concurrency-safe and its Scan match uses raw
// strings.Contains (so the literal "embed:*" never matches real keys) — this
// stub prefix-matches like the production contract and supports multi-cursor
// iteration plus error injection for Clear/GetStats paths.
type stubPagedRedis struct {
	mu       sync.Mutex
	data     map[string]string
	pages    map[uint64]stubRedisPage
	deleted  []string
	scanErr  map[uint64]error
	delFail  bool
	getFail  bool
	setCount atomic.Int64
}

// stubRedisPage is one scripted SCAN response.
type stubRedisPage struct {
	keys []string
	next uint64
}

func newStubPagedRedis() *stubPagedRedis {
	return &stubPagedRedis{
		data:    make(map[string]string),
		pages:   make(map[uint64]stubRedisPage),
		scanErr: make(map[uint64]error),
	}
}

func stubFilterPrefix(keys []string, match string) []string {
	prefix := match
	if len(prefix) > 0 && prefix[len(prefix)-1] == '*' {
		prefix = prefix[:len(prefix)-1]
	}
	var out []string
	for _, k := range keys {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, k)
		}
	}
	return out
}

func (s *stubPagedRedis) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getFail {
		return "", errors.New("stub redis get failure")
	}
	val, ok := s.data[key]
	if !ok {
		return "", errors.New("stub redis: key not found")
	}
	return val, nil
}

func (s *stubPagedRedis) Set(_ context.Context, key string, value interface{}, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.data[key] = string(raw)
	s.setCount.Add(1)
	return nil
}

func (s *stubPagedRedis) Del(_ context.Context, keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.delFail {
		return errors.New("stub redis del failure")
	}
	for _, k := range keys {
		delete(s.data, k)
		s.deleted = append(s.deleted, k)
	}
	return nil
}

func (s *stubPagedRedis) Keys(_ context.Context, _ string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data))
	for k := range s.data {
		out = append(out, k)
	}
	return out, nil
}

func (s *stubPagedRedis) Scan(_ context.Context, cursor uint64, match string, _ int64) (uint64, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.scanErr[cursor]; ok && err != nil {
		return 0, nil, err
	}
	page, ok := s.pages[cursor]
	if !ok {
		return 0, nil, nil
	}
	return page.next, stubFilterPrefix(page.keys, match), nil
}

func (s *stubPagedRedis) deletedKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deleted...)
}

// TestFallbackClientEmbed_HTTPSuccess locks the happy path: a live embedding
// service short-circuits before any fallback strategy runs.
func TestFallbackClientEmbed_HTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"embedding":[0.1,0.2],"dimension":2}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := NewEmbeddingClient(srv.URL, "stub-model", nil, time.Second)
	fb := NewFallbackClient(client, FallbackToCache)

	vec, err := fb.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 2 || vec[0] != 0.1 || vec[1] != 0.2 {
		t.Fatalf("unexpected embedding %v", vec)
	}
}

// newDeadFallbackClient builds a FallbackClient whose HTTP backend is
// unreachable, forcing the fallback strategies to run.
func newDeadFallbackClient(strategy FallbackStrategy, redis RedisClient) *FallbackClient {
	client := NewEmbeddingClient("http://127.0.0.1:1", "stub-model", redis, time.Second)
	return NewFallbackClient(client, strategy)
}

// TestFallbackClientEmbed_CacheFallbackHit verifies FallbackToCache serves a
// seeded redis entry when the HTTP service is down.
func TestFallbackClientEmbed_CacheFallbackHit(t *testing.T) {
	redis := NewMockRedisClient()
	fb := newDeadFallbackClient(FallbackToCache, redis)

	key := fb.client.getCacheKey("cache me", "query:")
	if err := redis.Set(context.Background(), key, []float64{0.3, 0.4}, time.Minute); err != nil {
		t.Fatalf("seed redis: %v", err)
	}

	vec, err := fb.Embed(context.Background(), "cache me")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 2 || vec[0] != 0.3 || vec[1] != 0.4 {
		t.Fatalf("unexpected cached embedding %v", vec)
	}
}

// TestFallbackClientEmbed_CacheMissReturnsErrEmbeddingFailed pins the
// cache-miss contract: FallbackToCache with no entry yields
// ErrEmbeddingFailed so the caller can route to keyword search.
func TestFallbackClientEmbed_CacheMissReturnsErrEmbeddingFailed(t *testing.T) {
	fb := newDeadFallbackClient(FallbackToCache, NewMockRedisClient())

	_, err := fb.Embed(context.Background(), "never cached")
	if !errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("err = %v, want ErrEmbeddingFailed", err)
	}
}

// TestFallbackClientEmbed_CorruptCacheJSON covers the unmarshal-failure path
// inside getFromCache.
func TestFallbackClientEmbed_CorruptCacheJSON(t *testing.T) {
	redis := NewMockRedisClient()
	fb := newDeadFallbackClient(FallbackToCache, redis)

	key := fb.client.getCacheKey("corrupt", "query:")
	redis.data[key] = "not-valid-json"

	_, err := fb.Embed(context.Background(), "corrupt")
	if !errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("err = %v, want ErrEmbeddingFailed", err)
	}
}

// TestFallbackClientEmbed_NilRedisCacheFallback covers the redis==nil guard
// in getFromCache.
func TestFallbackClientEmbed_NilRedisCacheFallback(t *testing.T) {
	fb := newDeadFallbackClient(FallbackToCache, nil)

	_, err := fb.Embed(context.Background(), "no redis")
	if !errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("err = %v, want ErrEmbeddingFailed", err)
	}
}

// TestFallbackClientEmbed_KeywordStrategy pins FallbackToKeyword: the client
// returns ErrEmbeddingFailed so the retrieval layer switches to keyword mode.
func TestFallbackClientEmbed_KeywordStrategy(t *testing.T) {
	fb := newDeadFallbackClient(FallbackToKeyword, NewMockRedisClient())

	_, err := fb.Embed(context.Background(), "keyword route")
	if !errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("err = %v, want ErrEmbeddingFailed", err)
	}
}

// TestFallbackClientEmbed_ErrorStrategyReturnsOriginalError locks
// FallbackToError: the caller sees the underlying client error, not the
// sentinel.
func TestFallbackClientEmbed_ErrorStrategyReturnsOriginalError(t *testing.T) {
	fb := newDeadFallbackClient(FallbackToError, nil)

	_, err := fb.Embed(context.Background(), "propagate")
	if err == nil {
		t.Fatal("expected original client error, got nil")
	}
	if errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("FallbackToError must not rewrite to ErrEmbeddingFailed, got %v", err)
	}

	fb.SetStrategy(FallbackStrategy(99))
	_, err = fb.Embed(context.Background(), "unknown strategy")
	if err == nil {
		t.Fatal("expected original client error for unknown strategy, got nil")
	}
	if errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("unknown strategy must propagate original error, got %v", err)
	}
}

// TestFallbackClientEmbedBatch_AllCacheHits covers getBatchFromCache when
// every requested text is present in redis.
func TestFallbackClientEmbedBatch_AllCacheHits(t *testing.T) {
	redis := NewMockRedisClient()
	fb := newDeadFallbackClient(FallbackToCache, redis)

	texts := []string{"batch a", "batch b"}
	want := [][]float64{{0.1, 0.1}, {0.2, 0.2}}
	for i, text := range texts {
		key := fb.client.getCacheKey(text, "query:")
		if err := redis.Set(context.Background(), key, want[i], time.Minute); err != nil {
			t.Fatalf("seed redis %q: %v", text, err)
		}
	}

	got, err := fb.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d embeddings, want %d", len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != 2 || got[i][0] != want[i][0] {
			t.Fatalf("embedding[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestFallbackClientEmbedBatch_PartialCacheMiss pins the all-or-nothing
// contract: one missing cache key fails the whole batch.
func TestFallbackClientEmbedBatch_PartialCacheMiss(t *testing.T) {
	redis := NewMockRedisClient()
	fb := newDeadFallbackClient(FallbackToCache, redis)

	key := fb.client.getCacheKey("present", "query:")
	if err := redis.Set(context.Background(), key, []float64{0.5}, time.Minute); err != nil {
		t.Fatalf("seed redis: %v", err)
	}

	_, err := fb.EmbedBatch(context.Background(), []string{"present", "absent"})
	if !errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("err = %v, want ErrEmbeddingFailed", err)
	}
}

// TestFallbackClientEmbedBatch_KeywordAndErrorStrategies covers the batch
// strategy switch arms.
func TestFallbackClientEmbedBatch_KeywordAndErrorStrategies(t *testing.T) {
	fb := newDeadFallbackClient(FallbackToKeyword, nil)
	_, err := fb.EmbedBatch(context.Background(), []string{"x"})
	if !errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("keyword strategy err = %v, want ErrEmbeddingFailed", err)
	}

	fb.SetStrategy(FallbackToError)
	_, err = fb.EmbedBatch(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("expected original client error, got nil")
	}
	if errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("error strategy must not rewrite to ErrEmbeddingFailed, got %v", err)
	}
}

// TestFallbackClientSetStrategyVisibleToEmbed locks the live strategy swap:
// SetStrategy must take effect on the next Embed call.
func TestFallbackClientSetStrategyVisibleToEmbed(t *testing.T) {
	fb := newDeadFallbackClient(FallbackToCache, nil)

	fb.SetStrategy(FallbackToKeyword)
	if got := fb.GetStrategy(); got != FallbackToKeyword {
		t.Fatalf("GetStrategy = %v, want FallbackToKeyword", got)
	}
	_, err := fb.Embed(context.Background(), "swap")
	if !errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("after keyword strategy err = %v, want ErrEmbeddingFailed", err)
	}

	fb.SetStrategy(FallbackToError)
	_, err = fb.Embed(context.Background(), "swap again")
	if err == nil {
		t.Fatal("expected original client error, got nil")
	}
	if errors.Is(err, ErrEmbeddingFailed) {
		t.Fatalf("after error strategy err = %v, want original client error", err)
	}
}

// TestFallbackClientEmbed_StrategyRaceWithSetStrategy hammers SetStrategy
// concurrently with Embed on the FallbackToError path (no redis read) to
// exercise the RLock/Lock split under go test -race.
func TestFallbackClientEmbed_StrategyRaceWithSetStrategy(t *testing.T) {
	fb := newDeadFallbackClient(FallbackToError, newStubPagedRedis())

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			fb.SetStrategy(FallbackToError)
			fb.SetStrategy(FallbackStrategy(99))
		}
	}()

	var embedErrs atomic.Int64
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := fb.Embed(context.Background(), "race"); err == nil {
				embedErrs.Add(1)
			}
		}
	}()

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()

	if n := embedErrs.Load(); n != 0 {
		t.Fatalf("dead HTTP backend must never succeed, got %d successes", n)
	}
}
