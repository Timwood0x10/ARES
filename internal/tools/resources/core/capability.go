package core

// Capability represents a task capability that tools can perform.
type Capability string

const (
	// CapabilityMath represents mathematical calculation capability.
	CapabilityMath Capability = "math"
	// CapabilityKnowledge represents knowledge retrieval and management capability.
	CapabilityKnowledge Capability = "knowledge"
	// CapabilityMemory represents memory access and storage capability.
	CapabilityMemory Capability = "memory"
	// CapabilityText represents text processing and manipulation capability.
	CapabilityText Capability = "text"
	// CapabilityNetwork represents network request and API interaction capability.
	CapabilityNetwork Capability = "network"
	// CapabilityTime represents date and time operations capability.
	CapabilityTime Capability = "time"
	// CapabilityFile represents file system operations capability.
	CapabilityFile Capability = "file"
	// CapabilityExternal represents external system interaction capability.
	CapabilityExternal Capability = "external"
)
