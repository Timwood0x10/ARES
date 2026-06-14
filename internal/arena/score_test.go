package arena

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCalculateScore_PerfectRecovery(t *testing.T) {
	stats := Stats{
		TotalActions:      10,
		SuccessfulActions: 10,
		FailedActions:     0,
	}
	score := CalculateScore(stats, 500*time.Millisecond)

	assert.InDelta(t, 100.0, score.Score, 0.1)
	assert.Equal(t, "A+", score.Grade)
	assert.InDelta(t, 100.0, score.RecoveryRate, 0.01)
	assert.Equal(t, 10, score.TotalFaults)
	assert.Equal(t, 10, score.RecoveredFaults)
	assert.Equal(t, 0, score.FailedFaults)
}

func TestCalculateScore_ZeroActions(t *testing.T) {
	stats := Stats{}
	score := CalculateScore(stats, 0)

	// With no actions, recovery rate is 0, speed score is 0.
	// Score = 0*0.7 + 0*0.3 = 0.
	assert.InDelta(t, 0.0, score.Score, 0.1)
	assert.Equal(t, "F", score.Grade)
	assert.Equal(t, 0, score.TotalFaults)
}

func TestCalculateScore_FullFailure(t *testing.T) {
	stats := Stats{
		TotalActions:      10,
		SuccessfulActions: 0,
		FailedActions:     10,
	}
	score := CalculateScore(stats, 2*time.Second)

	// Recovery rate = 0%, speed score = 0 (no successful recoveries).
	// Score = 0*0.7 + 0*0.3 = 0.
	assert.InDelta(t, 0.0, score.Score, 0.1)
	assert.Equal(t, "F", score.Grade)
	assert.InDelta(t, 0.0, score.RecoveryRate, 0.01)
	assert.Equal(t, 10, score.FailedFaults)
}

func TestCalculateScore_SlowRecovery(t *testing.T) {
	stats := Stats{
		TotalActions:      10,
		SuccessfulActions: 10,
		FailedActions:     0,
	}
	// At 10s avg recovery, speed score = 0.
	score := CalculateScore(stats, 10*time.Second)

	// Score = 100*0.7 + 0*0.3 = 70.
	assert.InDelta(t, 70.0, score.Score, 0.1)
	assert.Equal(t, "C", score.Grade)
}

func TestCalculateScore_GradeThresholds(t *testing.T) {
	tests := []struct {
		name   string
		score  float64
		grade  string
	}{
		{"A+ boundary", 95.0, "A+"},
		{"A boundary", 90.0, "A"},
		{"B boundary", 80.0, "B"},
		{"C boundary", 70.0, "C"},
		{"D boundary", 60.0, "D"},
		{"F boundary", 59.9, "F"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grade := gradeFromScore(tt.score)
			assert.Equal(t, tt.grade, grade)
		})
	}
}

func TestCalculateScore_SpeedScoreLinearDecay(t *testing.T) {
	// All actions succeed to isolate speed impact.
	stats := Stats{
		TotalActions:      10,
		SuccessfulActions: 10,
		FailedActions:     0,
	}

	// At 1s, speed = 100, score = 100*0.7 + 100*0.3 = 100.
	score1s := CalculateScore(stats, 1*time.Second)
	assert.InDelta(t, 100.0, score1s.Score, 0.1)

	// At 5.5s (midpoint), speed = 50, score = 100*0.7 + 50*0.3 = 85.
	scoreMid := CalculateScore(stats, 5500*time.Millisecond)
	assert.InDelta(t, 85.0, scoreMid.Score, 0.5)

	// At 10s, speed = 0, score = 100*0.7 + 0*0.3 = 70.
	score10s := CalculateScore(stats, 10*time.Second)
	assert.InDelta(t, 70.0, score10s.Score, 0.1)
}

func TestCalculateScore_PartialRecovery(t *testing.T) {
	stats := Stats{
		TotalActions:      10,
		SuccessfulActions: 8,
		FailedActions:     2,
	}
	score := CalculateScore(stats, 1*time.Second)

	// Recovery rate = 80%, speed = 100.
	// Score = 80*0.7 + 100*0.3 = 56 + 30 = 86.
	assert.InDelta(t, 86.0, score.Score, 0.1)
	assert.Equal(t, "B", score.Grade)
	assert.InDelta(t, 80.0, score.RecoveryRate, 0.01)
}

func TestCalculateScore_FastRecovery(t *testing.T) {
	stats := Stats{
		TotalActions:      20,
		SuccessfulActions: 19,
		FailedActions:     1,
	}
	score := CalculateScore(stats, 200*time.Millisecond)

	// Recovery rate = 95%, speed = 100.
	// Score = 95*0.7 + 100*0.3 = 66.5 + 30 = 96.5.
	assert.InDelta(t, 96.5, score.Score, 0.1)
	assert.Equal(t, "A+", score.Grade)
}
