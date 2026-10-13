package agentfabric

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestAnswerCognitionCarriesEndingCause pins the D1/D2 visibility contract on
// the consumer side: the terminal result names WHY the session ended, so an
// answer forced by the depth guard or by L1 skipping every tool call is
// distinguishable from a normal completion. Before this, all of them were a
// success-looking COMPLETED result and the cause lived only in a log line.
func TestAnswerCognitionCarriesEndingCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       map[string]any
		want       string
		wantSource string
	}{
		{
			name:       "normal completion is labelled planned",
			args:       map[string]any{answerContentKey: "final answer", answerCauseKey: answerCausePlanned},
			want:       answerCausePlanned,
			wantSource: answerBodySupplied,
		},
		{
			// The two facts are independent: growth stopped because of the depth
			// bound, and the emitted body is the gap. Collapsing them into one
			// label (an earlier cut of this fix) erased the cause here.
			name:       "depth-capped answer keeps its cause even when the body is a gap",
			args:       map[string]any{answerCauseKey: answerCauseDepthCapped, answerTruncatedKey: true},
			want:       answerCauseDepthCapped,
			wantSource: answerBodyGap,
		},
		{
			name:       "L1-blocked answer keeps its cause",
			args:       map[string]any{answerContentKey: "closing turn", answerCauseKey: answerCauseL1Blocked},
			want:       answerCauseL1Blocked,
			wantSource: answerBodySupplied,
		},
		{
			// A node grown by a direct/SDK caller carries content but no stamp:
			// a supplied answer is what this path can vouch for.
			name:       "unstamped node with content falls back to planned",
			args:       map[string]any{answerContentKey: "final answer"},
			want:       answerCausePlanned,
			wantSource: answerBodySupplied,
		},
		{
			// No stamp and no content: growth stopped for a reason nobody
			// recorded, so the cause is reported as unattributed instead of being
			// guessed as "planned". The actionable fact — that no answer was
			// produced — is in the body source.
			name:       "unstamped content-less node is unattributed with a gap body",
			args:       map[string]any{},
			want:       answerCauseUnlabeled,
			wantSource: answerBodyGap,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			task := &models.Task{
				TaskID:    "task-cause",
				AgentType: models.AgentType("ares/plan"),
			}
			if len(tt.args) > 0 {
				payload := make(map[string]any, len(tt.args))
				for k, v := range tt.args {
					payload[argMetadataPrefix+k] = v
				}
				task.Payload = payload
			}

			out, err := (&answerCognition{}).ExecuteStep(context.Background(), task)
			if err != nil {
				t.Fatalf("ExecuteStep: %v", err)
			}
			if !out.Done {
				t.Fatal("the answer node must terminate the session (Done=true)")
			}
			if got := out.Result.Metadata[answerCauseKey]; got != tt.want {
				t.Fatalf("cause = %v, want %q (metadata: %v)", got, tt.want, out.Result.Metadata)
			}
			if got := out.Result.Metadata[answerBodySourceKey]; got != tt.wantSource {
				t.Fatalf("body source = %v, want %q (metadata: %v)", got, tt.wantSource, out.Result.Metadata)
			}
		})
	}
}
