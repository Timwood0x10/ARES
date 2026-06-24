package models

import (
	"fmt"
	"time"
)

// Gender represents user gender.
type Gender string

const (
	GenderMale   Gender = "male"
	GenderFemale Gender = "female"
	GenderOther  Gender = "other"
)

// StyleTag represents user preference style tags.
type StyleTag string

const (
	Sporty          StyleTag = "sporty"
	StyleMinimalist StyleTag = "minimalist"
	StyleVintage    StyleTag = "vintage"
	StyleBohemian   StyleTag = "bohemian"
)

// Occasion represents usage scenarios.
type Occasion string

const (
	OccasionWork     Occasion = "work"
	OccasionSports   Occasion = "sports"
	OccasionFormal   Occasion = "formal"
	OccasionVacation Occasion = "vacation"
)

// SessionStatus represents session state.
type SessionStatus string

const (
	SessionStatusPending    SessionStatus = "pending"
	SessionStatusProcessing SessionStatus = "processing"
	SessionStatusCompleted  SessionStatus = "completed"
	SessionStatusFailed     SessionStatus = "failed"
	SessionStatusExpired    SessionStatus = "expired"
)

// AgentType represents agent types.
type AgentType string

const (
	AgentTypeLeader AgentType = "leader"
	AgentTypeTop    AgentType = "agent_top"
	AgentTypeBottom AgentType = "agent_bottom"

	// Travel agent types
	AgentTypeDestination AgentType = "destination"
	AgentTypeFood        AgentType = "food"
	AgentTypeHotel       AgentType = "hotel"
	AgentTypeItinerary   AgentType = "itinerary"
)

// AgentStatus represents agent running state.
type AgentStatus string

const (
	AgentStatusStarting AgentStatus = "starting"
	AgentStatusReady    AgentStatus = "ready"
	AgentStatusBusy     AgentStatus = "busy"
	AgentStatusStopping AgentStatus = "stopping"
	AgentStatusOffline  AgentStatus = "offline"
)

// ParseAgentStatus parses a string into an AgentStatus.
// Returns an error if the string does not match any known status.
//
// WARNING: On error, the returned AgentStatus is an empty string ("")
// which does NOT match any valid AgentStatus constant. Callers MUST
// check the returned error before using the status value.
//
// Args:
//
//	s - status string (e.g., "ready", "busy", "offline").
//
// Returns:
//
//	status - parsed AgentStatus (empty string on error).
//	err - error if the string is not a valid AgentStatus.
func ParseAgentStatus(s string) (AgentStatus, error) {
	switch AgentStatus(s) {
	case AgentStatusStarting:
		return AgentStatusStarting, nil
	case AgentStatusReady:
		return AgentStatusReady, nil
	case AgentStatusBusy:
		return AgentStatusBusy, nil
	case AgentStatusStopping:
		return AgentStatusStopping, nil
	case AgentStatusOffline:
		return AgentStatusOffline, nil
	default:
		return "", fmt.Errorf("invalid agent status: %q", s)
	}
}

// PriceRange represents budget range.
type PriceRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// NewPriceRange creates a new PriceRange.
func NewPriceRange(min, max float64) *PriceRange {
	return &PriceRange{Min: min, Max: max}
}

// IsValid checks if the price range is valid.
func (p *PriceRange) IsValid() bool {
	return p != nil && p.Min >= 0 && p.Max >= p.Min
}

// Contains checks if the price is within range.
func (p *PriceRange) Contains(price float64) bool {
	if !p.IsValid() {
		return false
	}
	return price >= p.Min && price <= p.Max
}

// Time fields for tracking.
var (
	DefaultSessionTTL = 24 * time.Hour
	DefaultTaskTTL    = 1 * time.Hour
)
