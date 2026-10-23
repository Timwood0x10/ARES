package context

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The chat path's REF half: retaining the original bytes of a compressed
// observation so the chat history can be REPLAYABLE, not merely IDENTIFIABLE.
//
// The contract this file implements (see compressObservation for the other
// side): when a bulky message is stored, its verbatim content is kept in the
// session's artifact table and the message carries a ref that resolves back to
// it. Cleaning then compresses the message as before, but the marker it emits
// can name a ref that actually dereferences — the same shape+digest+ref quartet
// the planner path gets from its L2 node ids.
//
// Three properties make the ref honest rather than decorative, and each one is
// structural, not a convention:
//
//  1. The ref is minted at WRITE time, by the layer that has both the session
//     identity and the bytes. A compressing reader (the cleaner) cannot mint
//     one: it holds a message slice and nothing to point at.
//  2. Artifacts live INSIDE SessionData, so their lifetime IS the session's.
//     TTL expiry, Delete and Clear drop the bytes with the messages; there is no
//     second GC to fall out of sync and no way to have a ref whose blob outlived
//     its session or vice versa.
//  3. Every path that drops a message drops the artifact it pointed at (count
//     cap, token cap, byte-cap eviction), so a ref can never survive its blob.
//     A ref that cannot be dereferenced is worse than no ref at all: it reads
//     as recoverable evidence and is not.
//
// What a ref does NOT survive: a caller holding a COPY of the messages (Get /
// GetMessages return copies) can render a ref after the live session evicted
// its blob. Such a read fails with ErrArtifactNotFound — an honest failure, and
// the reason the reader surface is an error rather than an empty string.

const (
	// ArtifactScheme prefixes every ref, so a ref is recognisable in a prompt
	// and cannot be confused with a node id or a URL.
	ArtifactScheme = "artifact://"

	// defaultMaxArtifactBytes bounds the verbatim originals retained per
	// session. Artifacts are a second copy of bytes the message slice already
	// holds (the compressed message keeps a gist), so this is the knob that
	// keeps "replayable history" from meaning "unbounded memory". Oldest
	// artifacts are evicted first, and their refs are stripped from the
	// messages that point at them.
	defaultMaxArtifactBytes = 512 << 10 // 512 KiB

	// artifactMinRunes is the smallest content worth retaining. Below it the
	// cleaner's gist loses almost nothing, so a ref would cost more than the
	// bytes it protects.
	artifactMinRunes = 64
)

var (
	// ErrArtifactNotFound means the ref is well-formed but its bytes are gone:
	// the session expired, the artifact was evicted by the byte cap, or the
	// caller holds a stale message copy. Retry is pointless; the caller should
	// fall back to the compressed form it already has.
	ErrArtifactNotFound = errors.New("artifact not found")

	// ErrInvalidArtifactRef means the string is not an artifact ref this store
	// could ever have minted.
	ErrInvalidArtifactRef = errors.New("invalid artifact ref")
)

// artifactState is the per-session artifact table. It lives inside SessionData
// on purpose (see the property list above); the fields are unexported so no
// serializer or hand-built SessionData can invent a table that skips the
// eviction rules.
type artifactState struct {
	blobs map[string]string
	// order is FIFO by creation, so eviction is oldest-first without a
	// timestamp per entry.
	order []string
	bytes int
	// seq is per-session and monotonic; it only has to be unique within the
	// session because the session id is part of the ref.
	seq uint64
}

// artifactRefFor renders the ref for one stored blob.
func artifactRefFor(sessionID string, seq uint64) string {
	return ArtifactScheme + sessionID + "/" + strconv.FormatUint(seq, 10)
}

// ArtifactSessionID extracts the session a ref belongs to.
//
// The separator is the LAST '/' because session ids in this codebase are
// path-like ("sess/1/plan/0" is a node id shape), so splitting on the first
// would truncate the session and silently scope an ownership check to the wrong
// session.
func ArtifactSessionID(ref string) (string, bool) {
	if !strings.HasPrefix(ref, ArtifactScheme) {
		return "", false
	}
	rest := strings.TrimPrefix(ref, ArtifactScheme)
	i := strings.LastIndex(rest, "/")
	if i <= 0 || i == len(rest)-1 {
		return "", false
	}
	if _, err := strconv.ParseUint(rest[i+1:], 10, 64); err != nil {
		return "", false
	}
	return rest[:i], true
}

// ensureArtifacts lazily initialises the table. Sessions created before this
// feature (or by any future constructor) must not panic on first write.
func (s *SessionData) ensureArtifacts() *artifactState {
	if s.artifacts == nil {
		s.artifacts = &artifactState{blobs: make(map[string]string)}
	}
	return s.artifacts
}

// put stores content and returns the ref that dereferences it, plus the refs it
// had to evict to stay under maxBytes.
//
// The evicted list is returned rather than swallowed because eviction is the
// one place the store decides a ref is dead: the caller must strip those refs
// from the messages that carry them, or the prompt keeps advertising bytes the
// store no longer holds.
func (s *SessionData) put(content string, maxBytes int) (ref string, evicted []string) {
	state := s.ensureArtifacts()
	state.seq++
	ref = artifactRefFor(s.SessionID, state.seq)
	state.blobs[ref] = content
	state.order = append(state.order, ref)
	state.bytes += len(content)

	// Evict oldest-first until the table fits. A single oversized payload can
	// evict itself; the caller checks whether its ref survived before stamping
	// it on the message.
	for state.bytes > maxBytes && len(state.order) > 0 {
		oldest := state.order[0]
		state.order = state.order[1:]
		if blob, ok := state.blobs[oldest]; ok {
			state.bytes -= len(blob)
			delete(state.blobs, oldest)
			evicted = append(evicted, oldest)
		}
	}
	return ref, evicted
}

// resolve returns the blob for ref, or ErrArtifactNotFound.
func (s *SessionData) resolve(ref string) (string, error) {
	if s.artifacts == nil {
		return "", ErrArtifactNotFound
	}
	blob, ok := s.artifacts.blobs[ref]
	if !ok {
		return "", ErrArtifactNotFound
	}
	return blob, nil
}

// holds reports whether the blob for ref is still retained.
func (s *SessionData) holds(ref string) bool {
	if s.artifacts == nil {
		return false
	}
	_, ok := s.artifacts.blobs[ref]
	return ok
}

// drop releases one blob. The ref stays on any message that already carries it
// unless the caller also strips it — releaseRefs does both, and is what the
// drop paths use.
func (s *SessionData) drop(ref string) {
	if s.artifacts == nil {
		return
	}
	if blob, ok := s.artifacts.blobs[ref]; ok {
		s.artifacts.bytes -= len(blob)
		delete(s.artifacts.blobs, ref)
	}
	for i, r := range s.artifacts.order {
		if r == ref {
			s.artifacts.order = append(s.artifacts.order[:i], s.artifacts.order[i+1:]...)
			break
		}
	}
}

// releaseRefs drops the blobs behind refs AND removes those refs from every
// message, so the store never keeps a ref it cannot resolve.
func (s *SessionData) releaseRefs(refs []string) {
	if len(refs) == 0 {
		return
	}
	dead := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		s.drop(ref)
		dead[ref] = struct{}{}
	}
	if len(dead) == 0 {
		return
	}
	for i := range s.Messages {
		s.Messages[i].ArtifactRefs = withoutRefs(s.Messages[i].ArtifactRefs, dead)
	}
}

// releaseUnreferenced drops every blob no surviving message points at. It is
// the sweep the message-drop paths call after they shrink the slice: the count
// cap and token cap cut messages without naming them, so "what is referenced
// now" is the only reliable question.
func (s *SessionData) releaseUnreferenced() {
	if s.artifacts == nil || len(s.artifacts.blobs) == 0 {
		return
	}
	live := make(map[string]struct{}, len(s.Messages))
	for i := range s.Messages {
		for _, ref := range s.Messages[i].ArtifactRefs {
			live[ref] = struct{}{}
		}
	}
	for ref := range s.artifacts.blobs {
		if _, ok := live[ref]; !ok {
			s.drop(ref)
		}
	}
}

// stripDeadRefs removes refs whose blobs the table does not hold. Used when a
// message slice arrives from outside (Set) and could carry refs minted by a
// table that no longer exists.
func (s *SessionData) stripDeadRefs() {
	if len(s.Messages) == 0 {
		return
	}
	for i := range s.Messages {
		refs := s.Messages[i].ArtifactRefs
		if len(refs) == 0 {
			continue
		}
		// A NEW slice, not refs[:0]: Get/GetMessages hand out shallow Message
		// copies whose ArtifactRefs share this backing array, so compacting in
		// place would rewrite a caller's view behind its back.
		kept := make([]string, 0, len(refs))
		for _, ref := range refs {
			if s.holds(ref) {
				kept = append(kept, ref)
			}
		}
		if len(kept) == 0 {
			kept = nil
		}
		s.Messages[i].ArtifactRefs = kept
	}
}

// withoutRefs returns refs minus dead, preserving order and never returning a
// non-nil-but-empty slice (JSON omitempty would otherwise emit []).
//
// It allocates: Message copies handed out by Get/GetMessages share the
// ArtifactRefs backing array, so an in-place compaction would mutate a reader's
// slice.
func withoutRefs(refs []string, dead map[string]struct{}) []string {
	if len(refs) == 0 {
		return refs
	}
	kept := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, gone := dead[ref]; gone {
			continue
		}
		kept = append(kept, ref)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// stampArtifact retains msg.Content verbatim and puts a ref on the message.
//
// Only tool OBSERVATIONS are retained: a user prompt is the question (already
// in the transcript and cheap to keep whole), an assistant turn is intent prose
// (replayed by the cleaner directly), and a tool_call's content is not what its
// marker renders — see artifactWorthy. The observation is the bulk the cleaner
// compresses and the thing worth re-reading.
func (m *SessionMemory) stampArtifact(session *SessionData, msg *Message) {
	if !artifactWorthy(*msg) {
		return
	}
	if session.UserID == "" {
		// An unattributed session has no owner the read side can verify against,
		// so a ref here could never pass the ownership check. Minting one anyway
		// would advertise evidence that is guaranteed to be refused — the exact
		// "ref that reads as recoverable but is not" this design forbids. The
		// marker degrades to identity-only (digest), which is the honest form.
		return
	}
	max := m.maxArtifactBytes
	if max <= 0 {
		max = defaultMaxArtifactBytes
	}
	ref, evicted := session.put(msg.Content, max)
	// Eviction already happened inside the table; strip the dead refs from the
	// stored messages so nobody is handed one. This runs BEFORE msg is appended,
	// so the new message cannot be caught by its own eviction sweep.
	session.releaseRefs(evicted)
	if !session.holds(ref) {
		// The payload alone exceeded the cap and evicted itself. Do not stamp a
		// ref that resolves to nothing.
		return
	}
	msg.ArtifactRefs = append(msg.ArtifactRefs, ref)
}

// artifactWorthy reports whether a message's content is payload-shaped: a role
// whose content the cleaner compresses, and content big enough that the gist
// loses something.
//
// Only RoleToolResult is retained. RoleToolCall was included in the first cut
// and should not be: the tool-call marker (summarizeToolCall) renders the call's
// arguments, not its content, and never consumes ArtifactRefs — so a retained
// tool_call blob would occupy the 512 KiB cap and be evicted-and-advertised by
// nothing. A ref nobody renders is worse than no ref: it spends retention
// budget protecting bytes that never reach a prompt. The assistant's prose is
// the intent anyway, which NormalizeIntent replays cheaply without a blob.
func artifactWorthy(msg Message) bool {
	if msg.Role != RoleToolResult {
		return false
	}
	return utf8.RuneCountInString(msg.Content) >= artifactMinRunes
}

// GetArtifact resolves a ref to the verbatim content it identifies.
//
// It applies the same lazy TTL check as GetMessages: an expired session is
// deleted (artifacts with it) and the ref fails with ErrArtifactNotFound
// instead of resurrecting a session nobody is using.
func (m *SessionMemory) GetArtifact(ctx context.Context, ref string) (string, error) {
	sessionID, ok := ArtifactSessionID(ref)
	if !ok {
		return "", ErrInvalidArtifactRef
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return "", ErrArtifactNotFound
	}
	if time.Since(session.AccessedAt) > m.ttl {
		delete(m.sessions, sessionID)
		return "", ErrArtifactNotFound
	}
	session.AccessedAt = time.Now()

	blob, err := session.resolve(ref)
	if err != nil {
		return "", err
	}
	return blob, nil
}

// ArtifactStats reports what a session currently retains, for the operator
// surface and for tests that assert the bound holds.
type ArtifactStats struct {
	Count int
	Bytes int
}

// ArtifactStats returns the retention numbers for one session. A missing or
// expired session reports the zero value, like GetMessages' not-found case.
func (m *SessionMemory) ArtifactStats(ctx context.Context, sessionID string) ArtifactStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[sessionID]
	if !ok || session.artifacts == nil {
		return ArtifactStats{}
	}
	return ArtifactStats{Count: len(session.artifacts.blobs), Bytes: session.artifacts.bytes}
}

// WithMaxArtifactBytes sets the per-session artifact cap (0 or negative
// restores the default). Returns the receiver for chaining.
func (m *SessionMemory) WithMaxArtifactBytes(n int) *SessionMemory {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxArtifactBytes = n
	return m
}
