package data

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordLLMCall_NilPayload(t *testing.T) {
	ca := NewCostAggregator()
	// Should not panic.
	ca.RecordLLMCall("a1", nil)
	cost := ca.GetCost("a1")
	assert.Equal(t, int64(0), cost.TotalTokens)
	assert.Equal(t, 0, cost.CallCount)
}

func TestRecordLLMCall_EmptyAgentID(t *testing.T) {
	ca := NewCostAggregator()
	// Should not panic and should not record.
	ca.RecordLLMCall("", map[string]any{"input_tokens": float64(100)})
	cost := ca.GetCost("")
	assert.Equal(t, int64(0), cost.TotalTokens)
}

func TestRecordLLMCall_SingleCall(t *testing.T) {
	ca := NewCostAggregator()
	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(100),
		"output_tokens":  float64(50),
		"estimated_cost": 0.005,
	})

	cost := ca.GetCost("a1")
	assert.Equal(t, int64(100), cost.InputTokens)
	assert.Equal(t, int64(50), cost.OutputTokens)
	assert.Equal(t, int64(150), cost.TotalTokens)
	assert.InDelta(t, 0.005, cost.EstimatedCost, 0.0001)
	assert.Equal(t, 1, cost.CallCount)
	assert.Equal(t, "USD", cost.Currency)
}

func TestRecordLLMCall_MultipleCallsSameAgent(t *testing.T) {
	ca := NewCostAggregator()
	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(100),
		"output_tokens":  float64(50),
		"estimated_cost": 0.005,
	})
	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(200),
		"output_tokens":  float64(100),
		"estimated_cost": 0.010,
	})

	cost := ca.GetCost("a1")
	assert.Equal(t, int64(300), cost.InputTokens)
	assert.Equal(t, int64(150), cost.OutputTokens)
	assert.Equal(t, int64(450), cost.TotalTokens)
	assert.InDelta(t, 0.015, cost.EstimatedCost, 0.0001)
	assert.Equal(t, 2, cost.CallCount)
}

func TestRecordLLMCall_MultipleAgents(t *testing.T) {
	ca := NewCostAggregator()
	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(100),
		"output_tokens":  float64(50),
		"estimated_cost": 0.005,
	})
	ca.RecordLLMCall("a2", map[string]any{
		"input_tokens":   float64(200),
		"output_tokens":  float64(100),
		"estimated_cost": 0.010,
	})

	cost1 := ca.GetCost("a1")
	assert.Equal(t, int64(150), cost1.TotalTokens)
	assert.InDelta(t, 0.005, cost1.EstimatedCost, 0.0001)

	cost2 := ca.GetCost("a2")
	assert.Equal(t, int64(300), cost2.TotalTokens)
	assert.InDelta(t, 0.010, cost2.EstimatedCost, 0.0001)
}

func TestTotalCost(t *testing.T) {
	ca := NewCostAggregator()
	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(100),
		"output_tokens":  float64(50),
		"estimated_cost": 0.005,
	})
	ca.RecordLLMCall("a2", map[string]any{
		"input_tokens":   float64(200),
		"output_tokens":  float64(100),
		"estimated_cost": 0.010,
	})

	total := ca.TotalCost()
	assert.Equal(t, int64(300), total.InputTokens)
	assert.Equal(t, int64(150), total.OutputTokens)
	assert.Equal(t, int64(450), total.TotalTokens)
	assert.InDelta(t, 0.015, total.EstimatedCost, 0.0001)
	assert.Equal(t, 2, total.CallCount)
	assert.Equal(t, "USD", total.Currency)
}

func TestTotalCost_Empty(t *testing.T) {
	ca := NewCostAggregator()
	total := ca.TotalCost()
	assert.Equal(t, int64(0), total.TotalTokens)
	assert.InDelta(t, 0, total.EstimatedCost, 0.0001)
	assert.Equal(t, 0, total.CallCount)
	assert.Equal(t, "USD", total.Currency)
}

func TestCostBreakdown(t *testing.T) {
	ca := NewCostAggregator()
	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(100),
		"output_tokens":  float64(50),
		"estimated_cost": 0.005,
	})
	ca.RecordLLMCall("a2", map[string]any{
		"input_tokens":   float64(200),
		"output_tokens":  float64(100),
		"estimated_cost": 0.010,
	})

	bd := ca.CostBreakdown()
	assert.Len(t, bd.ByAgent, 2)
	assert.InDelta(t, 0.015, bd.Total, 0.0001)
	assert.Equal(t, "USD", bd.Currency)

	a1Cost, ok := bd.ByAgent["a1"]
	require.True(t, ok)
	assert.Equal(t, int64(150), a1Cost.TotalTokens)
}

func TestCostBreakdown_Empty(t *testing.T) {
	ca := NewCostAggregator()
	bd := ca.CostBreakdown()
	assert.Empty(t, bd.ByAgent)
	assert.InDelta(t, 0, bd.Total, 0.0001)
	assert.Equal(t, "USD", bd.Currency)
}

func TestSetAlert_CheckAlerts(t *testing.T) {
	ca := NewCostAggregator()
	ca.SetAlert("a1", 0.01)

	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(1000),
		"output_tokens":  float64(500),
		"estimated_cost": 0.05,
	})

	alerts := ca.CheckAlerts()
	require.Len(t, alerts, 1)
	assert.Equal(t, "a1", alerts[0].AgentID)
	assert.InDelta(t, 0.01, alerts[0].Threshold, 0.0001)
	assert.InDelta(t, 0.05, alerts[0].Actual, 0.0001)
	assert.Equal(t, "warning", alerts[0].Severity)
	assert.NotEmpty(t, alerts[0].Message)
	assert.NotEmpty(t, alerts[0].ID)
}

func TestCheckAlerts_NotExceeded(t *testing.T) {
	ca := NewCostAggregator()
	ca.SetAlert("a1", 1.0)

	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(100),
		"output_tokens":  float64(50),
		"estimated_cost": 0.005,
	})

	alerts := ca.CheckAlerts()
	assert.Empty(t, alerts)
}

func TestCheckAlerts_NoCostData(t *testing.T) {
	ca := NewCostAggregator()
	ca.SetAlert("a1", 0.01)
	// No LLM calls recorded for a1.
	alerts := ca.CheckAlerts()
	assert.Empty(t, alerts)
}

func TestSetAlert_InvalidInput(t *testing.T) {
	ca := NewCostAggregator()
	// Empty agent ID.
	ca.SetAlert("", 1.0)
	// Zero threshold.
	ca.SetAlert("a1", 0)
	// Negative threshold.
	ca.SetAlert("a1", -1.0)

	alerts := ca.CheckAlerts()
	assert.Empty(t, alerts)
}

func TestSetAlert_Overwrite(t *testing.T) {
	ca := NewCostAggregator()
	ca.SetAlert("a1", 0.01)
	ca.SetAlert("a1", 0.10)

	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   float64(1000),
		"output_tokens":  float64(500),
		"estimated_cost": 0.05,
	})

	alerts := ca.CheckAlerts()
	// 0.05 < 0.10, so no alert should fire.
	assert.Empty(t, alerts)
}

func TestCostAggregator_GetCost_NotFound(t *testing.T) {
	ca := NewCostAggregator()
	cost := ca.GetCost("missing")
	assert.Equal(t, int64(0), cost.TotalTokens)
	assert.Equal(t, 0, cost.CallCount)
}

func TestRecordLLMCall_IntValues(t *testing.T) {
	ca := NewCostAggregator()
	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   100,
		"output_tokens":  50,
		"estimated_cost": 0.005,
	})

	cost := ca.GetCost("a1")
	assert.Equal(t, int64(100), cost.InputTokens)
	assert.Equal(t, int64(50), cost.OutputTokens)
	assert.Equal(t, int64(150), cost.TotalTokens)
}

func TestRecordLLMCall_Int64Values(t *testing.T) {
	ca := NewCostAggregator()
	ca.RecordLLMCall("a1", map[string]any{
		"input_tokens":   int64(100),
		"output_tokens":  int64(50),
		"estimated_cost": float64(0.005),
	})

	cost := ca.GetCost("a1")
	assert.Equal(t, int64(100), cost.InputTokens)
	assert.Equal(t, int64(50), cost.OutputTokens)
}

func TestRecordLLMCall_MissingFields(t *testing.T) {
	ca := NewCostAggregator()
	ca.RecordLLMCall("a1", map[string]any{})

	cost := ca.GetCost("a1")
	assert.Equal(t, int64(0), cost.InputTokens)
	assert.Equal(t, int64(0), cost.OutputTokens)
	assert.Equal(t, int64(0), cost.TotalTokens)
	assert.InDelta(t, 0, cost.EstimatedCost, 0.0001)
	// CallCount should still increment.
	assert.Equal(t, 1, cost.CallCount)
}

func TestConcurrentAccess_CostAggregator(t *testing.T) {
	ca := NewCostAggregator()
	var wg sync.WaitGroup

	// Writers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := fmt.Sprintf("a%d", id%10)
			ca.RecordLLMCall(agentID, map[string]any{
				"input_tokens":   float64(100),
				"output_tokens":  float64(50),
				"estimated_cost": 0.005,
			})
		}(i)
	}

	// Readers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = ca.GetCost(fmt.Sprintf("a%d", id%10))
			_ = ca.TotalCost()
			_ = ca.CostBreakdown()
			_ = ca.CheckAlerts()
		}(i)
	}

	wg.Wait()

	total := ca.TotalCost()
	assert.Equal(t, 50, total.CallCount)
}

func TestConcurrentAccess_SetAlert(t *testing.T) {
	ca := NewCostAggregator()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := fmt.Sprintf("a%d", id%10)
			ca.SetAlert(agentID, float64(id)*0.01)
		}(i)
	}

	wg.Wait()
	// No assertions needed; the test verifies no race or panic.
}
