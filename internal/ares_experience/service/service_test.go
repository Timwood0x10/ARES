package experience

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConflictResolver(t *testing.T) {
	r := NewConflictResolver()
	require.NotNil(t, r)

	err := r.Configure(0.8)
	require.NoError(t, err)

	err = r.Configure(0.0)
	require.Error(t, err)

	err = r.Configure(1.5)
	require.Error(t, err)
}

func TestRankingService(t *testing.T) {
	rs := NewRankingService()
	require.NotNil(t, rs)

	w := DefaultRankingWeights()
	require.NotNil(t, w)
	require.InDelta(t, 0.05, w.UsageWeight, 1e-9)
	require.InDelta(t, 0.05, w.RecencyWeight, 1e-9)
	require.InDelta(t, 30.0, w.RecencyDays, 1e-9)

	err := rs.Configure(nil)
	require.NoError(t, err)

	err = rs.Configure(&RankingWeights{UsageWeight: -1, RecencyWeight: 0.05, RecencyDays: 30})
	require.Error(t, err)

	err = rs.Configure(&RankingWeights{UsageWeight: 0.1, RecencyWeight: -1, RecencyDays: 30})
	require.Error(t, err)

	err = rs.Configure(&RankingWeights{UsageWeight: 0.1, RecencyWeight: 0.1, RecencyDays: 0})
	require.Error(t, err)

	err = rs.Configure(&RankingWeights{UsageWeight: 0.1, RecencyWeight: 0.1, RecencyDays: 30})
	require.NoError(t, err)
}

func TestRankedExperienceTypes(t *testing.T) {
	var e *Experience
	_ = e
	var re *RankedExperience
	_ = re

	require.Equal(t, "success", ExperienceTypeSuccess)
	require.Equal(t, "failure", ExperienceTypeFailure)
}

func TestTaskResultType(t *testing.T) {
	tr := &TaskResult{Task: "test", Result: "output", Success: true}
	require.True(t, tr.Success)
	require.Equal(t, "test", tr.Task)
	require.Equal(t, "output", tr.Result)

	var tr2 *TaskResult
	require.Nil(t, tr2)
}

func TestExperienceGetUsageCount(t *testing.T) {
	e := &Experience{UsageCount: 5}
	require.Equal(t, 5, e.GetUsageCount())

	e2 := &Experience{}
	require.Equal(t, 0, e2.GetUsageCount())
}

func TestExtractedExperienceType(t *testing.T) {
	e := &ExtractedExperience{
		Problem:     "p",
		Solution:    "s",
		Constraints: "c",
	}
	require.Equal(t, "p", e.Problem)
	require.Equal(t, "s", e.Solution)
	require.Equal(t, "c", e.Constraints)
}

func TestNewDistillationService(t *testing.T) {
	svc := NewDistillationService(nil, nil, nil)
	require.NotNil(t, svc)
}
