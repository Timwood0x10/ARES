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

// Plan creates an execution plan from capability requirements.
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

	// Build capability to step ID mapping for dependency resolution.
	capaToStepID := make(map[string]string)
	for _, req := range requirements {
		stepID := fmt.Sprintf("step-%s-%d", req.Name, len(capaToStepID))
		capaToStepID[req.Name] = stepID
	}

	for _, req := range requirements {
		stepID := capaToStepID[req.Name]
		params := defaultParamsFor(req.Name)

		depStepIDs := make([]string, 0, len(req.DependsOn))
		for _, dep := range req.DependsOn {
			if depID, ok := capaToStepID[dep]; ok {
				depStepIDs = append(depStepIDs, depID)
			} else {
				depStepIDs = append(depStepIDs, dep)
			}
		}

		steps = append(steps, ExecutionStep{
			StepID:            stepID,
			ToolName:          req.ResolvedTool,
			CapabilityName:    req.Name,
			Parameters:        params,
			DependsOn:         depStepIDs,
			FallbackToolNames: fallbackToolsFor(req.Name),
		})
		totalCost += 3
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
	case "Arithmetic", "Summation", "DiscreteMath", "Probability", "NumberTheory":
		return map[string]interface{}{"expression": ""}
	case "Statistics":
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

	// Aggregate by tool:capability key.
	type accum struct {
		successCount float64
		failureCount float64
		latencySum   float64
		totalCount   float64
	}
	accums := make(map[string]*accum)

	for _, e := range allEvidence {
		key := e.ToolName + ":" + e.CapabilityName
		a, ok := accums[key]
		if !ok {
			a = &accum{}
			accums[key] = a
		}
		a.totalCount++
		a.latencySum += float64(e.Latency.Microseconds())
		if e.Success {
			a.successCount++
		} else {
			a.failureCount++
		}
	}

	for key, a := range accums {
		if a.totalCount == 0 {
			continue
		}
		successRate := a.successCount / a.totalCount
		avgLatency := a.latencySum / a.totalCount / 1000.0
		baseScore := 10.0
		evidenceScore := successRate*20.0 - avgLatency/100.0
		penalty := a.failureCount / a.totalCount * 10.0

		result[key] = ToolScore{
			BaseScore:     baseScore,
			EvidenceScore: evidenceScore,
			Penalty:       penalty,
			Final:         baseScore + evidenceScore - penalty,
		}
	}

	return result, nil
}
