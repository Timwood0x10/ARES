// production_session_cache_test.go locks the session-cache fixes:
//
//   - REVIEW 3.3#10: a zero MaxSessions config evicted every freshly
//     created session immediately (len(cache) 1 > 0), so every message was
//     attributed to "anonymous". The cap now falls back to
//     defaultMaxSessions when unset.
//   - REVIEW 3.3#11: sessions existed only in the in-memory cache, so after
//     a manager restart every message was re-attributed to "anonymous".
//     resolveSessionUserID now recovers the user from the persisted
//     conversation history and re-heals the cache.
package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// fakeConversationStore backs the manager with an in-memory conversation
// history, standing in for the PostgreSQL repository.
type fakeConversationStore struct {
	bySession  map[string][]*storage_models.Conversation
	getCalls   int
	created    []*storage_models.Conversation
	deletedIDs []string
}

func (f *fakeConversationStore) Create(ctx context.Context, conv *storage_models.Conversation) error {
	f.created = append(f.created, conv)
	return nil
}

func (f *fakeConversationStore) GetBySession(ctx context.Context, sessionID, tenantID string, limit int) ([]*storage_models.Conversation, error) {
	f.getCalls++
	rows := f.bySession[sessionID]
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (f *fakeConversationStore) DeleteBySession(ctx context.Context, sessionID, tenantID string) (int64, error) {
	f.deletedIDs = append(f.deletedIDs, sessionID)
	delete(f.bySession, sessionID)
	return 1, nil
}

func newSessionCacheTestManager(maxSessions int, store *fakeConversationStore) *ProductionMemoryManager {
	return &ProductionMemoryManager{
		config:                 &MemoryConfig{MaxSessions: maxSessions, SessionTTL: 24 * 3600e9},
		sessionCache:           make(map[string]*SessionData),
		conversationRepository: store,
		currentTenantID:        "tenant-a",
	}
}

func TestSessionCacheZeroCapDoesNotEvictFreshSession(t *testing.T) {
	m := newSessionCacheTestManager(0, &fakeConversationStore{})

	m.mu.Lock()
	m.addSessionToCacheLocked("s1", "user-1")
	m.mu.Unlock()

	m.mu.RLock()
	_, exists := m.sessionCache["s1"]
	m.mu.RUnlock()
	assert.True(t, exists, "a session must survive insertion when MaxSessions is unset (zero)")
}

func TestSessionCacheEvictsOverCap(t *testing.T) {
	m := newSessionCacheTestManager(2, &fakeConversationStore{})

	m.mu.Lock()
	m.addSessionToCacheLocked("s1", "user-1")
	m.addSessionToCacheLocked("s2", "user-2")
	// Give the entries distinct recency: s1 oldest, s2 newest. Without this
	// the two time.Now() stamps can be identical, making the LRU pick
	// map-iteration-random.
	m.sessionCache["s1"].UpdatedAt = time.Now().Add(-1 * time.Hour)
	m.mu.Unlock()

	// Insertion of a 3rd session must evict the LRU entry (s1).
	m.mu.Lock()
	m.addSessionToCacheLocked("s3", "user-3")
	m.mu.Unlock()

	m.mu.RLock()
	size := len(m.sessionCache)
	_, s1Exists := m.sessionCache["s1"]
	_, s3Exists := m.sessionCache["s3"]
	m.mu.RUnlock()

	assert.Equal(t, 2, size, "cache must be capped at MaxSessions")
	assert.False(t, s1Exists, "least recently used entry must be evicted")
	assert.True(t, s3Exists, "newest entry must survive")
}

func TestResolveSessionUserIDRecoversFromHistory(t *testing.T) {
	store := &fakeConversationStore{
		bySession: map[string][]*storage_models.Conversation{
			"s-restarted": {
				{SessionID: "s-restarted", TenantID: "tenant-a", UserID: "user-42"},
			},
		},
	}
	// Empty cache: simulates a manager restart with persisted history.
	m := newSessionCacheTestManager(10, store)

	userID := m.resolveSessionUserID(context.Background(), "s-restarted")
	assert.Equal(t, "user-42", userID,
		"cache miss must recover the user from conversation history, not default to anonymous")

	// The cache must self-heal so the next message does not repeat the
	// history lookup.
	m.mu.RLock()
	cached, exists := m.sessionCache["s-restarted"]
	m.mu.RUnlock()
	require.True(t, exists, "successful recovery must repopulate the cache")
	assert.Equal(t, "user-42", cached.UserID)

	callsAfterRecovery := store.getCalls
	userID = m.resolveSessionUserID(context.Background(), "s-restarted")
	assert.Equal(t, "user-42", userID)
	assert.Equal(t, callsAfterRecovery, store.getCalls,
		"the self-healed cache must serve subsequent lookups without another history read")
}

func TestResolveSessionUserIDAnonymousOnlyWithoutHistory(t *testing.T) {
	m := newSessionCacheTestManager(10, &fakeConversationStore{})

	userID := m.resolveSessionUserID(context.Background(), "unknown-session")
	assert.Equal(t, "anonymous", userID,
		"no cache entry and no history must still fall back to anonymous")
}
