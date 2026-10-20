//go:build integration

package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/ares_config"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/kernel"
	"github.com/Timwood0x10/ares/internal/llm"
)

// Live checks that need a REAL provider. Run with:
//
//	go test -tags integration ./internal/agentruntime/ -run TestLive -v
//
// Credentials come from the repository's own ares.yaml (the deployment config at the
// repo root), so these run against the same provider, endpoint and model the server
// would use — a stub would prove the wiring and nothing about whether it WORKS.
//
// Nothing here prints the api_key; the logs carry provider/model/base_url only, which
// are not secrets.
//
// SKIPPING: with no ares.yaml (or no api_key in it) these SKIP with a reason rather than
// passing quietly. "Verified nothing" must never look like "verified fine" — that is
// the distinction this evaluation path exists to keep straight.

// *llm.Client already satisfies the runtime's chat contract, which is why a candidate
// runtime can be handed the live client with no adapter in between. Pinned here so the
// two shapes cannot drift apart silently: if either side changes, this line stops
// compiling instead of the live test failing for a reason nobody expects.
var _ agentfabric.ChatClient = (*llm.Client)(nil)

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test directory")
		}
		dir = parent
	}
}

// liveClient builds the real client from the repo's ares.yaml, or skips with the reason.
func liveClient(t *testing.T) *llm.Client {
	t.Helper()
	path := filepath.Join(repoRoot(t), "ares.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no deployment config at %s: %v", path, err)
	}
	cfg, err := ares_config.Load(path)
	require.NoError(t, err)
	if strings.TrimSpace(cfg.LLM.APIKey) == "" {
		t.Skipf("%s configures no llm.api_key", path)
	}

	client, err := llm.NewClient(&llm.Config{
		Provider: cfg.LLM.Provider,
		APIKey:   cfg.LLM.APIKey,
		BaseURL:  cfg.LLM.BaseURL,
		Model:    cfg.LLM.Model,
		Timeout:  cfg.LLM.Timeout,
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)

	t.Logf("live provider: provider=%s model=%s base_url=%s fallbacks=%d",
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.BaseURL, len(cfg.LLM.Fallbacks))
	return client
}

// TestLive_ConfiguredProviderAnswers is the credential smoke test every other live
// check rests on: it proves the endpoint is reachable and the key accepted, so a later
// failure means "the evaluation path broke" rather than "the config is stale".
func TestLive_ConfiguredProviderAnswers(t *testing.T) {
	client := liveClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	answer, err := client.Generate(ctx, "Reply with exactly the word PONG and nothing else.")
	require.NoError(t, err, "the configured provider must answer")
	assert.NotEmpty(t, strings.TrimSpace(answer), "an empty completion is not a working provider")
	t.Logf("live answer: %q", strings.TrimSpace(answer))
}

// TestLive_CandidateRuntimeRefusesASilentEnd drives ONE case through a candidate
// runtime assembled over the deployment's REAL client.
//
// The executor is scripted and returns no answer, so the assertion is the invariant the
// accuracy signal rests on: a case whose execution body produces nothing must surface as
// an ERROR, never as an empty answer. An empty string would be scored as a
// well-formed-but-wrong answer, so "nothing ran" would read as "the candidate answered
// badly" — and a whole evaluation would silently rank candidates on a failure it
// reported as a low score.
//
// What this does NOT cover, deliberately: a live case doing real agent work. Those
// capability executors are assembled by cmd/ares, where the serving kernel lives. A
// scripted executor reported as "the runtime is verified end to end" would be measuring
// the stub.
func TestLive_CandidateRuntimeRefusesASilentEnd(t *testing.T) {
	client := liveClient(t)

	cfg := CandidateRuntimeConfig{
		ChatClient: client, // real provider, no adapter
		ToolBinder: stubToolBinder{},
		Executors: func(core *Execution, _ *taskfabric.Fabric, _ *agentfabric.Fabric) (map[string]kernel.CapabilityExecutor, error) {
			// The real adapter over the real router, rather than a scripted body: this is
			// the shape the wiring will use, and it keeps the live test on that path.
			return DefaultCandidateExecutors(core)
		},
		Await: AwaitOptions{Wait: 2 * time.Second, PollInterval: 20 * time.Millisecond},
	}
	rt, err := NewCandidateRuntime(cfg, &agents.ActiveStrategy{ID: "live-candidate", Prompt: "Be terse."})
	require.NoError(t, err, "the runtime must accept the deployment's real client")
	defer rt.Close()

	rt.Start(context.Background())
	require.Eventually(t, func() bool { return rt.sched.Running() },
		2*time.Second, 5*time.Millisecond, "the candidate's own loop must drain its own fabric")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answer, _, _, err := rt.Executor().Execute(ctx, "what is 2+2?")
	require.ErrorIs(t, err, ErrSessionStalled,
		"a case that produces no answer must be an error, not an empty answer")
	assert.Empty(t, answer, "no answer text may be reported alongside the stall")
}
