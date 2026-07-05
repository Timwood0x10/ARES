package leader

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/llm/output"
)

// profileParser parses user profile from natural language input.
type profileParser struct {
	llmAdapter output.LLMAdapter
	template   *output.TemplateEngine
	promptTpl  string
	validator  *output.Validator
	maxRetries int
	eventStore ares_events.EventStore
}

// NewProfileParser creates a new ProfileParser with LLM support.
func NewProfileParser(
	llmAdapter output.LLMAdapter,
	template *output.TemplateEngine,
	promptTpl string,
	validator *output.Validator,
	maxRetries int,
) ProfileParser {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	return &profileParser{
		llmAdapter: llmAdapter,
		template:   template,
		promptTpl:  promptTpl,
		validator:  validator,
		maxRetries: maxRetries,
	}
}

// WithEventStore sets the event store for LLM call tracking.
func (p *profileParser) WithEventStore(store ares_events.EventStore) {
	p.eventStore = store
}

// emitEvent appends a single event using the canonical ares_events.Emit.
func (p *profileParser) emitEvent(ctx context.Context, eventType ares_events.EventType, payload map[string]any) {
	if ares_events.Emit(ctx, p.eventStore, "profile-parser", eventType, "leader", payload) {
		log.Debug("event emitted", "type", eventType)
	}
}

// Parse parses user input into UserProfile using LLM.
func (p *profileParser) Parse(ctx context.Context, input string) (*models.UserProfile, error) {
	// If no LLM adapter, return default profile
	if p.llmAdapter == nil {
		return p.getDefaultProfile(), nil
	}

	log.Debug("Parsing profile with LLM", "input", input)

	for attempt := 0; attempt < p.maxRetries; attempt++ {
		profile, err := p.parseOnce(ctx, input)
		if err != nil {
			log.Debug("Parse attempt failed", "attempt", attempt+1, "error", err)
			continue
		}

		// Validate result
		if err := p.validateProfile(profile); err != nil {
			log.Debug("Validate attempt failed", "attempt", attempt+1, "error", err)
			continue
		}

		log.Debug("Profile parsed successfully", "user_id", profile.UserID, "style", profile.Style)
		return profile, nil
	}

	// Fallback to default profile
	return p.getDefaultProfile(), nil
}

func (p *profileParser) getDefaultProfile() *models.UserProfile {
	return &models.UserProfile{
		Preferences: make(map[string]any),
	}
}

func (p *profileParser) parseOnce(ctx context.Context, input string) (*models.UserProfile, error) {
	// Render prompt
	prompt, err := p.template.Render(p.promptTpl, map[string]string{
		"input": input,
	})
	if err != nil {
		return nil, errors.WrapError(errors.ErrPromptRenderFailed, err)
	}

	// Call LLM
	response, err := p.llmAdapter.Generate(ctx, prompt)
	if err != nil {
		p.emitEvent(ctx, ares_events.EventLLMCall, map[string]any{
			"purpose": "profile_parsing",
			"model":   p.llmAdapter.GetModel(),
			"error":   err.Error(),
			"status":  "failed",
		})
		return nil, errors.WrapError(errors.ErrLLMGenerateFailed, err)
	}

	p.emitEvent(ctx, ares_events.EventLLMCall, map[string]any{
		"purpose":    "profile_parsing",
		"model":      p.llmAdapter.GetModel(),
		"input_len":  len(prompt),
		"output_len": len(response),
	})

	// Parse response
	profile, err := p.parseResponse(response)
	if err != nil {
		return nil, errors.WrapError(errors.ErrProfileParsingFailed, err)
	}

	return profile, nil
}

func (p *profileParser) parseResponse(response string) (*models.UserProfile, error) {
	// Debug: print raw response
	log.Debug("Raw LLM response", "preview", response[:min(500, len(response))])

	// Try to parse as JSON
	parser := output.NewParser()
	data, err := parser.ParseJSON(response)
	if err != nil {
		return nil, errors.WrapError(errors.ErrLLMParserFailed, err)
	}

	// Debug: print parsed data keys
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	log.Debug("Parsed data keys", "keys", keys)

	// Extract fields
	profile := &models.UserProfile{}

	// Parse style
	if style, ok := data["style"]; ok {
		if styles, ok := style.([]interface{}); ok {
			for _, s := range styles {
				if str, ok := s.(string); ok {
					profile.Style = append(profile.Style, models.StyleTag(str))
				}
			}
		}
	}

	// Parse occasions
	if occasions, ok := data["occasions"]; ok {
		if occs, ok := occasions.([]interface{}); ok {
			for _, o := range occs {
				if str, ok := o.(string); ok {
					profile.Occasions = append(profile.Occasions, models.Occasion(str))
				}
			}
		}
	}

	// Parse budget - support both number (e.g., 10000) and object (e.g., {"min": 5000, "max": 10000})
	if budget, ok := data["budget"]; ok && budget != nil {
		switch b := budget.(type) {
		case float64:
			// Budget is a number like 10000
			profile.Budget = models.NewPriceRange(0, b)
		case map[string]interface{}:
			// Budget is an object like {"min": 5000, "max": 10000}
			min := 0.0
			max := 10000.0
			if v, ok := b["min"]; ok {
				if f, ok := toFloat64(v); ok {
					min = f
				}
			}
			if v, ok := b["max"]; ok {
				if f, ok := toFloat64(v); ok {
					max = f
				}
			}
			profile.Budget = models.NewPriceRange(min, max)
		}
	}

	// Initialize Preferences map if nil
	if profile.Preferences == nil {
		profile.Preferences = make(map[string]any)
	}

	// Dynamically extract ALL fields from JSON response into Preferences
	// This makes the parser flexible for any scenario (travel, research, etc.)
	// The TaskPlanner then decides which agents to call based on triggers
	for key, value := range data {
		// Skip fields already parsed into dedicated fields
		if key == "style" || key == "occasions" || key == "budget" {
			continue
		}
		// Store all other fields in Preferences for trigger-based matching
		if value != nil {
			profile.Preferences[key] = value
		}
	}

	return profile, nil
}

func (p *profileParser) validateProfile(profile *models.UserProfile) error {
	if profile == nil {
		return errors.ErrNilPointer
	}
	if len(profile.Preferences) == 0 && len(profile.Style) == 0 {
		return errors.ErrProfileValidationFailed
	}
	return nil
}

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case string:
		var f float64
		_, err := fmt.Sscanf(val, "%f", &f)
		return f, err == nil
	}
	return 0, false
}
