package context

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newArtifactSession creates a session with the given store configured.
func newArtifactSession(t *testing.T, m *SessionMemory, sessionID string) {
	t.Helper()
	require.NoError(t, m.Set(context.Background(), sessionID, "user-1", nil))
}

// addToolResult stores a tool result message and returns the ref it was stamped
// with ("" when nothing was retained).
func addToolResult(t *testing.T, m *SessionMemory, sessionID, content string) string {
	t.Helper()
	require.NoError(t, m.AddMessage(context.Background(), sessionID, Message{
		Role:    RoleToolResult,
		Content: content,
	}))
	msgs, err := m.GetMessages(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	refs := msgs[len(msgs)-1].ArtifactRefs
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

// TestArtifactReplayableEndToEnd is the contract this whole file exists for: a
// compressed observation's ref must dereference to the ORIGINAL bytes, through
// the cleaner, so the chat path is replayable and not just identifiable.
func TestArtifactReplayableEndToEnd(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(100, time.Minute)
	newArtifactSession(t, m, "sess-1")

	original := strings.Repeat("warning: deprecated call site\n", 40) + "done\n"
	ref := addToolResult(t, m, "sess-1", original)
	require.NotEmpty(t, ref, "a bulky tool result must be retained and stamped")
	assert.True(t, strings.HasPrefix(ref, ArtifactScheme))

	// The prompt-facing form is compressed...
	msgs, err := m.GetMessages(ctx, "sess-1")
	require.NoError(t, err)
	cleaned := NewContextCleaner().Clean(msgs)
	require.Len(t, cleaned, 1)
	require.Contains(t, cleaned[0].Content, "sha256:")
	assert.Less(t, len([]rune(cleaned[0].Content)), len([]rune(original)))

	// ...and it names a ref that resolves to the verbatim bytes.
	require.Contains(t, cleaned[0].Content, "ref:"+ref)
	got, err := m.GetArtifact(ctx, ref)
	require.NoError(t, err, "the ref in the compressed marker must dereference")
	assert.Equal(t, original, got, "and must return the ORIGINAL, not the gist")
}

// TestArtifactMarkerDegradesWithoutRef pins the two grades: a message stored
// without retention (small, or a non-payload role, or evicted) compresses to a
// digest-only marker and must not claim a ref it does not have.
func TestArtifactMarkerDegradesWithoutRef(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(100, time.Minute)
	newArtifactSession(t, m, "sess-2")

	// Below the retention floor: nothing to protect, so no ref.
	if ref := addToolResult(t, m, "sess-2", "ok"); ref != "" {
		t.Fatalf("a tiny result must not be retained: %q", ref)
	}
	// A user prompt is the question, not a payload: not retained either.
	require.NoError(t, m.AddMessage(ctx, "sess-2", Message{Role: RoleUser, Content: strings.Repeat("q", 200)}))

	msgs, err := m.GetMessages(ctx, "sess-2")
	require.NoError(t, err)
	for _, msg := range msgs {
		assert.Empty(t, msg.ArtifactRefs, "role %s must not carry a ref", msg.Role)
	}
}

// TestArtifactEvictionStripsTheRef is the anti-lying rule: when the byte cap
// evicts a blob, the ref is removed from the message that pointed at it, so no
// prompt can advertise bytes the store no longer has.
func TestArtifactEvictionStripsTheRef(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(100, time.Minute).WithMaxArtifactBytes(150)
	newArtifactSession(t, m, "sess-3")

	first := addToolResult(t, m, "sess-3", strings.Repeat("a", 120))
	require.NotEmpty(t, first)
	second := addToolResult(t, m, "sess-3", strings.Repeat("b", 120))
	require.NotEmpty(t, second)

	stats := m.ArtifactStats(ctx, "sess-3")
	assert.Equal(t, 1, stats.Count, "the cap keeps only what fits")
	assert.LessOrEqual(t, stats.Bytes, 150)

	_, err := m.GetArtifact(ctx, first)
	assert.ErrorIs(t, err, ErrArtifactNotFound, "the evicted blob is gone")

	msgs, err := m.GetMessages(ctx, "sess-3")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Empty(t, msgs[0].ArtifactRefs, "the stale ref was stripped from its message")
	assert.Equal(t, []string{second}, msgs[1].ArtifactRefs)

	// The surviving ref still resolves.
	got, err := m.GetArtifact(ctx, second)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("b", 120), got)
}

// TestArtifactReleasedWhenMessageIsCapped: dropping a message must release the
// bytes it pointed at. The count cap cuts the slice without naming messages, so
// this asserts the "what is referenced now" sweep.
func TestArtifactReleasedWhenMessageIsCapped(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(100, time.Minute).WithMaxMessages(2)
	newArtifactSession(t, m, "sess-4")

	first := addToolResult(t, m, "sess-4", strings.Repeat("a", 100))
	require.NotEmpty(t, first)
	addToolResult(t, m, "sess-4", strings.Repeat("b", 100))
	addToolResult(t, m, "sess-4", strings.Repeat("c", 100))

	_, err := m.GetArtifact(ctx, first)
	assert.ErrorIs(t, err, ErrArtifactNotFound, "the dropped message's blob went with it")

	stats := m.ArtifactStats(ctx, "sess-4")
	assert.Equal(t, 2, stats.Count, "exactly the surviving messages' blobs")
}

// TestArtifactDiesWithItsSession: artifacts live inside the session, so TTL is
// the only GC needed. An expired ref fails closed instead of resurrecting.
func TestArtifactDiesWithItsSession(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(100, time.Minute)
	newArtifactSession(t, m, "sess-5")

	ref := addToolResult(t, m, "sess-5", strings.Repeat("x", 200))
	require.NotEmpty(t, ref)
	require.NoError(t, m.Set(ctx, "keepalive", "u", nil)) // an unrelated session keeps the store non-empty

	// Expire deterministically by ageing AccessedAt past the TTL, not by
	// sleeping on the wall clock: a 40ms sleep against a 20ms TTL is a CI flake
	// waiting for a loaded box.
	m.mu.Lock()
	m.sessions["sess-5"].AccessedAt = time.Now().Add(-2 * m.ttl)
	m.mu.Unlock()

	_, err := m.GetArtifact(ctx, ref)
	assert.ErrorIs(t, err, ErrArtifactNotFound)
	assert.Equal(t, ArtifactStats{}, m.ArtifactStats(ctx, "sess-5"))
}

// TestArtifactDeleteAndClear lock the two explicit removal paths: neither may
// leave a resolvable ref behind.
func TestArtifactDeleteAndClear(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(100, time.Minute)

	newArtifactSession(t, m, "d")
	refD := addToolResult(t, m, "d", strings.Repeat("d", 200))
	newArtifactSession(t, m, "c")
	refC := addToolResult(t, m, "c", strings.Repeat("c", 200))

	require.NoError(t, m.Delete(ctx, "d"))
	_, err := m.GetArtifact(ctx, refD)
	assert.ErrorIs(t, err, ErrArtifactNotFound)

	require.NoError(t, m.Clear(ctx))
	_, err = m.GetArtifact(ctx, refC)
	assert.ErrorIs(t, err, ErrArtifactNotFound)
}

// TestArtifactRefParsing covers the ref grammar, including the path-like
// session ids this codebase uses: splitting on the FIRST '/' would scope an
// ownership check to the wrong session.
func TestArtifactRefParsing(t *testing.T) {
	cases := []struct {
		ref     string
		session string
		ok      bool
	}{
		{ref: "artifact://sess1/7", session: "sess1", ok: true},
		{ref: "artifact://sess/1/plan/7", session: "sess/1/plan", ok: true},
		{ref: "artifact://sess1/", ok: false},
		{ref: "artifact://sess1/notanumber", ok: false},
		{ref: "artifact:///1", ok: false},
		{ref: "https://example.com/1", ok: false},
		{ref: "", ok: false},
	}
	for _, tc := range cases {
		session, ok := ArtifactSessionID(tc.ref)
		assert.Equal(t, tc.ok, ok, "ref %q", tc.ref)
		assert.Equal(t, tc.session, session, "ref %q", tc.ref)
	}
}

// TestGetArtifactRejectsForeignRefs: a string that is not one of ours must be
// distinguishable from a missing artifact, so a caller does not retry a typo.
func TestGetArtifactRejectsForeignRefs(t *testing.T) {
	m := NewSessionMemory(10, time.Minute)
	_, err := m.GetArtifact(context.Background(), "gc/plan/step/3")
	assert.True(t, errors.Is(err, ErrInvalidArtifactRef))
}

// TestSetStripsUnresolvableRefs: Set replaces the message slice, and a restored
// slice may carry refs minted by a table that no longer exists. Advertising them
// would be the same lie as an evicted ref.
func TestSetStripsUnresolvableRefs(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(10, time.Minute)

	require.NoError(t, m.Set(ctx, "restored", "u", []Message{{
		Role:         RoleToolResult,
		Content:      strings.Repeat("r", 200),
		ArtifactRefs: []string{"artifact://gone/1"},
	}}))

	msgs, err := m.GetMessages(ctx, "restored")
	require.NoError(t, err)
	assert.Empty(t, msgs[0].ArtifactRefs)
	_, err = m.GetArtifact(ctx, "artifact://gone/1")
	assert.ErrorIs(t, err, ErrArtifactNotFound)
}

// TestArtifactEvictionLeavesCallerCopiesAlone: Get/GetMessages hand out shallow
// Message copies whose ArtifactRefs share the store's backing array. Stripping
// an evicted ref must therefore allocate, not compact in place — otherwise the
// store rewrites a reader's slice behind its back.
//
// The stale copy keeps its ref on purpose; it resolves to ErrArtifactNotFound,
// which is the honest answer for a snapshot taken before the eviction.
func TestArtifactEvictionLeavesCallerCopiesAlone(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(10, time.Minute).WithMaxArtifactBytes(150)
	newArtifactSession(t, m, "sess-alias")

	first := addToolResult(t, m, "sess-alias", strings.Repeat("a", 120))
	require.NotEmpty(t, first)

	snapshot, err := m.GetMessages(ctx, "sess-alias")
	require.NoError(t, err)
	require.Len(t, snapshot[0].ArtifactRefs, 1)

	addToolResult(t, m, "sess-alias", strings.Repeat("b", 120)) // evicts `first`

	assert.Equal(t, []string{first}, snapshot[0].ArtifactRefs,
		"the caller's snapshot must not be rewritten by the store's eviction")

	live, err := m.GetMessages(ctx, "sess-alias")
	require.NoError(t, err)
	assert.Empty(t, live[0].ArtifactRefs, "but the live view no longer advertises it")

	_, err = m.GetArtifact(ctx, first)
	assert.ErrorIs(t, err, ErrArtifactNotFound, "a stale ref fails closed")
}

// TestArtifactNotMintedForAnUnattributedSession pins the ownership invariant at
// the WRITE side: an unattributed session has no owner the read side can verify
// against, so it must not mint a ref at all. Minting one would put a ref in a
// prompt that artifact_read is required to refuse — evidence shaped like it can
// be recovered when it cannot.
func TestArtifactNotMintedForAnUnattributedSession(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(10, time.Minute)
	require.NoError(t, m.Set(ctx, "anon", "", nil)) // no owner

	require.NoError(t, m.AddMessage(ctx, "anon", Message{
		Role:    RoleToolResult,
		Content: strings.Repeat("anonymous bulk\n", 100),
	}))

	msgs, err := m.GetMessages(ctx, "anon")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Empty(t, msgs[0].ArtifactRefs, "no owner ⇒ no ref ⇒ nothing to refuse later")
	assert.Equal(t, ArtifactStats{}, m.ArtifactStats(ctx, "anon"))

	// The content is still compressed; it just degrades to identity-only.
	cleaned := NewContextCleaner().Clean(msgs)
	assert.Contains(t, cleaned[0].Content, "sha256:", "the cut is still identifiable")
	assert.NotContains(t, cleaned[0].Content, "ref:", "but no unreadable ref is advertised")
}

// TestArtifactNotMintedForToolCallRoles pins ②: tool_call content is not what
// its marker renders, so retaining it would spend the byte cap on bytes no
// prompt ever points at.
func TestArtifactNotMintedForToolCallRoles(t *testing.T) {
	ctx := context.Background()
	m := NewSessionMemory(10, time.Minute)
	newArtifactSession(t, m, "sess-call")

	require.NoError(t, m.AddMessage(ctx, "sess-call", Message{
		Role:      RoleToolCall,
		Content:   strings.Repeat("call payload\n", 100),
		ToolCalls: []ToolCall{{ID: "c1", Function: ToolCallFunction{Name: "grep"}}},
	}))

	msgs, err := m.GetMessages(ctx, "sess-call")
	require.NoError(t, err)
	assert.Empty(t, msgs[0].ArtifactRefs, "a tool_call's ref would never be rendered")
	assert.Equal(t, ArtifactStats{}, m.ArtifactStats(ctx, "sess-call"))
}
