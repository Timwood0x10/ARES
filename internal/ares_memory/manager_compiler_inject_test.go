package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCompilerLifecycle builds a zero-LLM Conversation Compiler lifecycle for tests.
func newCompilerLifecycle(t *testing.T, cfg compiler.LifecycleConfig) *compiler.ContextLifecycle {
	t.Helper()
	kmc := compiler.NewCompiler(
		compiler.NewAKGExtractor(),
		compiler.NewRuleNormalizer(),
		compiler.DefaultCompileConfig(),
	)
	dist := compiler.NewKMDistiller()
	lc, err := compiler.NewContextLifecycle(kmc, dist, cfg)
	require.NoError(t, err)
	return lc
}

// newMemoryManager returns the concrete *memoryManager (the type actually wired
// by Bootstrap via ProvideMemory).
func newMemoryManager(t *testing.T) *memoryManager {
	t.Helper()
	mmIface, err := NewMemoryManager(DefaultMemoryConfig())
	require.NoError(t, err)
	mm, ok := mmIface.(*memoryManager)
	require.True(t, ok, "NewMemoryManager must return *memoryManager")
	return mm
}

// newSession creates a fresh session and returns its ID.
func newSession(t *testing.T, mm *memoryManager) string {
	t.Helper()
	sid, err := mm.CreateSession(context.Background(), "user")
	require.NoError(t, err)
	return sid
}

// findInjectedContext returns the first system message whose content is the
// compiler's rendered context block (contains the "## Context" header).
func findInjectedContext(msgs []Message) (Message, bool) {
	for _, m := range msgs {
		if m.Role == RoleSystem && strings.Contains(m.Content, "## Context") {
			return m, true
		}
	}
	return Message{}, false
}

// TestBuildPromptMessagesInjectsCompilerContext verifies broken-link-3 closure:
// when the compiler lifecycle is injected, BuildPromptMessages appends the
// rendered compressed context block as a system message.
func TestBuildPromptMessagesInjectsCompilerContext(t *testing.T) {
	ctx := context.Background()
	lc := newCompilerLifecycle(t, compiler.LifecycleConfig{})

	// Pre-compile a conversation so the lifecycle has content to render.
	_, _, err := lc.Compile(ctx, []compiler.SourceMessage{
		{Role: "user", Content: "We decided to use DECISIONMARKER_X for the runtime."},
	})
	require.NoError(t, err)

	mm := newMemoryManager(t)
	mm.SetKnowledgeCompiler(lc)
	sid := newSession(t, mm)

	msgs, err := mm.BuildPromptMessages(ctx, sid)
	require.NoError(t, err)
	injected, ok := findInjectedContext(msgs)
	require.True(t, ok, "injection must append the compiler context block")
	assert.Equal(t, RoleSystem, injected.Role)
}

// TestBuildPromptMessagesNoInjectionWhenDisabled is the Phase 3 regression gate:
// with the compiler disabled (nil lifecycle), BuildPromptMessages must NOT
// contain the compiler context block. Prior behavior is preserved.
func TestBuildPromptMessagesNoInjectionWhenDisabled(t *testing.T) {
	ctx := context.Background()
	mm := newMemoryManager(t) // lifecycle nil -> disabled

	// Empty session: no injected context block.
	sid := newSession(t, mm)
	msgs, err := mm.BuildPromptMessages(ctx, sid)
	require.NoError(t, err)
	_, ok := findInjectedContext(msgs)
	assert.False(t, ok, "disabled compiler must not inject any context block")

	// Session with a real message: the real message is present, still no
	// injected context block.
	sidReal := newSession(t, mm)
	require.NoError(t, mm.AddMessage(ctx, sidReal, "user", "real user message"))
	msgs, err = mm.BuildPromptMessages(ctx, sidReal)
	require.NoError(t, err)
	_, ok = findInjectedContext(msgs)
	assert.False(t, ok, "disabled compiler must not inject any context block")
	assert.Contains(t, messagesByContent(msgs, "real user message"), "real user message")
}

// TestBuildPromptMessagesAppendsAfterRealMessages verifies the injection is
// additive: existing messages are preserved and the context block is appended.
func TestBuildPromptMessagesAppendsAfterRealMessages(t *testing.T) {
	ctx := context.Background()
	lc := newCompilerLifecycle(t, compiler.LifecycleConfig{})
	_, _, err := lc.Compile(ctx, []compiler.SourceMessage{
		{Role: "user", Content: "We chose to adopt Rust as the primary language."},
	})
	require.NoError(t, err)

	mm := newMemoryManager(t)
	mm.SetKnowledgeCompiler(lc)
	sid := newSession(t, mm)
	require.NoError(t, mm.AddMessage(ctx, sid, "user", "a real user turn"))

	msgs, err := mm.BuildPromptMessages(ctx, sid)
	require.NoError(t, err)
	injected, ok := findInjectedContext(msgs)
	require.True(t, ok, "injection must append the compiler context block")
	assert.Equal(t, RoleSystem, injected.Role)
	assert.Contains(t, messagesByContent(msgs, "a real user turn"), "a real user turn")
}

// TestFeedCompilerTriggersCompile verifies the async feed path: AddMessage
// buffers the message and triggers an incremental compile once the token-window
// threshold is reached.
func TestFeedCompilerTriggersCompile(t *testing.T) {
	ctx := context.Background()
	// Tiny window so a single normal message triggers compilation.
	lc := newCompilerLifecycle(t, compiler.LifecycleConfig{
		WindowSize: 50,
		Threshold:  0.1,
	})
	mm := newMemoryManager(t)
	mm.SetKnowledgeCompiler(lc)
	sidFeed := newSession(t, mm)

	err := mm.AddMessage(ctx, sidFeed, "user",
		"hello world this is a sufficiently long test message to exceed the tiny threshold")
	require.NoError(t, err)

	// The compile runs in a detached goroutine; poll briefly for completion.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lc.CompileCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Greater(t, lc.CompileCount(), 0,
		"incremental compile should be triggered by the buffered message")

	// Sanity: the compiled content reached the lifecycle.
	block, err := lc.RenderPrompt(ctx, compiler.FormatMarkdown)
	require.NoError(t, err)
	assert.True(t, strings.Contains(block, "## Context"),
		"rendered block should be available for prompt injection")
}

// messagesByContent returns the content of the first message whose content
// contains substr, or "" if none.
func messagesByContent(msgs []Message, substr string) string {
	for _, m := range msgs {
		if strings.Contains(m.Content, substr) {
			return m.Content
		}
	}
	return ""
}
