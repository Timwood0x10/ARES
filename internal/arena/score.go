package arena

import (
	"time"
)

// ResilienceScore represents the system's resilience rating.
type ResilienceScore struct {
	RecoveryRate    float64       `json:"recovery_rate"`
	AvgRecoveryTime time.Duration `json:"avg_recovery_time"`
	TotalFaults     int           `json:"total_faults"`
	RecoveredFaults int           `json:"recovered_faults"`
	FailedFaults    int           `json:"failed_faults"`
	Score           float64       `json:"score"`
	Grade           string        `json:"grade"`
}

// CalculateScore computes resilience from arena stats and average recovery time.
// Score formula: Recovery Rate weight 70%, Recovery Speed weight 30%.
// Speed score is 100 when avg recovery <= 1s, linearly decaying to 0 at 10s.
// Speed score is 0 when there are no successful recoveries.
func CalculateScore(stats Stats, avgRecovery time.Duration) ResilienceScore {
	total := stats.TotalActions
	recovered := stats.SuccessfulActions
	failed := stats.FailedActions

	var recoveryRate float64
	if total > 0 {
		recoveryRate = float64(recovered) / float64(total) * 100
	}

	// Speed score: 100 at <= 1s, linearly decreasing to 0 at 10s.
	// No speed score when there are no successful recoveries.
	var speedScore float64
	avgSec := avgRecovery.Seconds()
	switch {
	case total == 0 || recovered == 0:
		speedScore = 0
	case avgSec <= 1.0:
		speedScore = 100
	case avgSec >= 10.0:
		speedScore = 0
	default:
		speedScore = (10.0 - avgSec) / 9.0 * 100
	}

	score := recoveryRate*0.7 + speedScore*0.3

	grade := gradeFromScore(score)

	return ResilienceScore{
		RecoveryRate:    recoveryRate,
		AvgRecoveryTime: avgRecovery,
		TotalFaults:     total,
		RecoveredFaults: recovered,
		FailedFaults:    failed,
		Score:           score,
		Grade:           grade,
	}
}

// gradeFromScore maps a 0-100 score to a letter grade.
func gradeFromScore(score float64) string {
	switch {
	case score >= 95:
		return "A+"
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	default:
		return "F"
	}
}
