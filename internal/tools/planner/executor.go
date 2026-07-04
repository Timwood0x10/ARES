package planner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// executionPlanner implements ExecutionPlanner for single-step and multi-step plans.
type executionPlanner struct{}

// NewExecutionPlanner creates a deterministic execution planner.
func NewExecutionPlanner() ExecutionPlanner {
	return &executionPlanner{}
}

// Plan creates an execution plan from capability requirements and their candidates.
// For multi-capability intents, it builds a DAG respecting dependency order.
func (p *executionPlanner) Plan(_ context.Context, intent *Intent, requirements []CapabilityRequirement) (*ExecutionPlan, error) {
	if intent == nil {
		return nil, fmt.Errorf("planner: intent is nil")
	}
	if len(requirements) == 0 {
		return nil, fmt.Errorf("planner: no requirements to plan")
	}

	planID := uuid.New().String()
	steps := make([]ExecutionStep, 0, len(requirements))
	totalCost := 0
	totalLatency := time.Duration(0)

	for _, req := range requirements {
		stepID := fmt.Sprintf("step-%s-%d", req.Name, len(steps))
		params := defaultParamsFor(req.Name)

		step := ExecutionStep{
			StepID:            stepID,
			ToolName:          "", // resolved at runtime after scoring
			CapabilityName:    req.Name,
			Parameters:        params,
			DependsOn:         req.DependsOn,
			FallbackToolNames: fallbackToolsFor(req.Name),
		}
		steps = append(steps, step)
		totalCost += 3 // default cost per step
		totalLatency += 100 * time.Millisecond
	}

	return &ExecutionPlan{
		PlanID:           planID,
		Intent:           *intent,
		Steps:            steps,
		IsMultiStep:      len(steps) > 1,
		Cost:             totalCost,
		EstimatedLatency: totalLatency,
	}, nil
}

// defaultParamsFor returns default parameter templates for a capability.
func defaultParamsFor(capability string) map[string]interface{} {
	switch capability {
	case "Arithmetic", "Summation":
		return map[string]interface{}{"expression": ""}
	case "PDFParsing":
		return map[string]interface{}{"operation": "extract_text", "file_path": ""}
	case "Hashing":
		return map[string]interface{}{"operation": "sha256", "input": ""}
	case "Base64":
		return map[string]interface{}{"operation": "base64_encode", "input": ""}
	case "StringManipulation":
		return map[string]interface{}{"operation": "upper", "input": ""}
	case "Regex":
		return map[string]interface{}{"operation": "match", "text": "", "pattern": ""}
	case "JSONProcessing":
		return map[string]interface{}{"operation": "parse", "data": ""}
	case "WebSearch":
		return map[string]interface{}{"query": ""}
	case "HTTPRequest":
		return map[string]interface{}{"url": ""}
	case "IDGeneration":
		return map[string]interface{}{"operation": "generate_uuid"}
	case "CodeExecution":
		return map[string]interface{}{"language": "", "code": ""}
	case "Embedding":
		return map[string]interface{}{"action": "embed", "text": ""}
	default:
		return map[string]interface{}{}
	}
}

// fallbackToolsFor returns fallback tool names for a capability.
func fallbackToolsFor(capability string) []string {
	switch capability {
	case "WebFetch":
		return []string{"web_search", "http_request"}
	default:
		return nil
	}
}

// memoryEvidenceStore implements EvidenceStore with in-memory storage.
type memoryEvidenceStore struct {
	mu       sync.RWMutex
	evidence []ToolEvidence
}

// NewMemoryEvidenceStore creates an in-memory evidence store.
func NewMemoryEvidenceStore() EvidenceStore {
	return &memoryEvidenceStore{
		evidence: make([]ToolEvidence, 0, 100),
	}
}

// Save records a tool execution result.
func (s *memoryEvidenceStore) Save(_ context.Context, evidence *ToolEvidence) error {
	if evidence == nil {
		return fmt.Errorf("planner: evidence is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidence = append(s.evidence, *evidence)
	return nil
}

// Query retrieves evidence matching the given criteria.
func (s *memoryEvidenceStore) Query(_ context.Context, toolName string, capabilityName string, limit int) ([]ToolEvidence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []ToolEvidence
	for _, e := range s.evidence {
		if toolName != "" && e.ToolName != toolName {
			continue
		}
		if capabilityName != "" && e.CapabilityName != capabilityName {
			continue
		}
		result = append(result, e)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

// Aggregate returns aggregate metrics per tool and capability.
func (s *memoryEvidenceStore) Aggregate(_ context.Context, toolName string) (map[string]ToolScore, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	allEvidence := s.evidence
	if toolName != "" {
		var filtered []ToolEvidence
		for _, e := range allEvidence {
			if e.ToolName == toolName {
				filtered = append(filtered, e)
			}
		}
		allEvidence = filtered
	}

	result := make(map[string]ToolScore)
	if len(allEvidence) == 0 {
		return result, nil
	}

	for _, e := range allEvidence {
		key := e.ToolName + ":" + e.CapabilityName
		ts := result[key]
		// Accumulate for averaging.
		// Store intermediate values using a custom type via the map.
		// Fields are mapped: baseScore = success count, evidenceScore = latency sum,
		// penalty = failure count, final = total count.
		ts2 := toolScoreAccumulator{
			ToolName:       e.ToolName,
			CapabilityName: e.CapabilityName,
			successCount:   result[key].Final,
			latencySum:     result[key].EvidenceScore,
			failureCount:   result[key].Penalty,
			totalCount:     result[key].BaseScore,
		}
		ts2.totalCount++
		if e.Success {
			ts2.successCount++
		} else {
			ts2.failureCount++
		}
		ts2.latencySum += float64(e.Latency.Microseconds())

		// Store intermediate values in the ToolScore fields.
		ts.Final = ts2.successCount
		ts.EvidenceScore = ts2.latencySum
		ts.Penalty = ts2.failureCount
		ts.BaseScore = ts2.totalCount
		result[key] = ts
	}

	// Convert accumulators to final metrics.
	for key, ts := range result {
		total := int(ts.BaseScore)
		if total == 0 {
			continue
		}
		successCount := int(ts.Final)
		failureCount := int(ts.Penalty)
		latencySum := ts.EvidenceScore

		// Compute final scores.
		successRate := float64(successCount) / float64(total)
		avgLatency := latencySum / float64(total) / 1000.0 // microseconds → milliseconds

		baseScore := 10.0
		evidenceScore := successRate*20.0 - avgLatency/100.0
		penalty := float64(failureCount) / float64(total) * 10.0

		result[key] = ToolScore{
			BaseScore:     baseScore,
			EvidenceScore: evidenceScore,
			Penalty:       penalty,
			Final:         baseScore + evidenceScore - penalty,
		}
	}

	return result, nil
}

// toolScoreAccumulator holds intermediate values for aggregate computation.
type toolScoreAccumulator struct {
	ToolName       string
	CapabilityName string
	successCount   float64
	failureCount   float64
	latencySum     float64
	totalCount     float64
}
