// Package client provides health check types and logic for the GoAgent API client.
package client

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/api/core"
)

// ServiceStatus represents the health status of a single service.
type ServiceStatus struct {
	// Available indicates whether the service is reachable and operational.
	Available bool `json:"available"`
	// Latency records the round-trip time of the last health probe.
	Latency time.Duration `json:"latency_ms"`
	// Error contains the error message if the service is unavailable.
	Error string `json:"error,omitempty"`
}

// HealthReport contains structured health information for all services.
type HealthReport struct {
	// LLMStatus reports the health of the LLM service.
	LLMStatus ServiceStatus `json:"llm"`
	// MemoryStatus reports the health of the memory service.
	MemoryStatus ServiceStatus `json:"memory"`
	// RetrievalStatus reports the health of the retrieval service.
	RetrievalStatus ServiceStatus `json:"retrieval"`
	// WorkflowStatus reports the health of the workflow service.
	WorkflowStatus ServiceStatus `json:"workflow"`
	// Healthy is true when all configured services are healthy.
	Healthy bool `json:"healthy"`
	// Timestamp records when this report was generated.
	Timestamp time.Time `json:"timestamp"`
}

// checkLLMHealth probes the LLM service availability and latency.
//
// Args:
//
//	ctx - operation context with timeout.
//	svc - LLM service instance (may be nil).
//
// Returns:
//
//	ServiceStatus with availability, latency, and error info.
func checkLLMHealth(ctx context.Context, svc core.LLMService) ServiceStatus {
	if svc == nil {
		return ServiceStatus{
			Available: false,
			Error:     "LLM service not configured",
		}
	}

	start := time.Now()
	available := svc.IsEnabled()
	latency := time.Since(start)

	status := ServiceStatus{
		Available: available,
		Latency:   latency,
	}
	if !available {
		status.Error = "LLM service not enabled"
	}
	return status
}

// checkServiceHealth probes a generic service by checking nil-ness.
//
// Args:
//
//	name - human-readable service name for error messages.
//	svc - any service pointer (checked for nil).
//
// Returns:
//
//	ServiceStatus indicating whether the service is configured.
func checkServiceHealth(name string, svc interface{}) ServiceStatus {
	if svc == nil {
		return ServiceStatus{
			Available: false,
			Error:     name + " service not configured",
		}
	}
	return ServiceStatus{
		Available: true,
	}
}

// buildHealthReport assembles individual service statuses into a full report.
//
// Args:
//
//	llm - LLM status.
//	memory - memory status.
//	retrieval - retrieval status.
//	workflow - workflow status.
//
// Returns:
//
//	HealthReport with overall status computed from all components.
func buildHealthReport(
	llm, memory, retrieval, workflow ServiceStatus,
) HealthReport {
	// Use var declaration for zero-value slice per Uber Go style ("nil Is a Valid Slice").
	var configured []ServiceStatus
	if llm.Error != "" || llm.Available {
		configured = append(configured, llm)
	}
	if memory.Error != "" || memory.Available {
		configured = append(configured, memory)
	}
	if retrieval.Error != "" || retrieval.Available {
		configured = append(configured, retrieval)
	}
	if workflow.Error != "" || workflow.Available {
		configured = append(configured, workflow)
	}

	allHealthy := true
	for _, s := range configured {
		if !s.Available {
			allHealthy = false
			break
		}
	}

	return HealthReport{
		LLMStatus:       llm,
		MemoryStatus:    memory,
		RetrievalStatus: retrieval,
		WorkflowStatus:  workflow,
		Healthy:         allHealthy,
		Timestamp:       time.Now().UTC(),
	}
}
