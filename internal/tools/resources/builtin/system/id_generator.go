package builtin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// IDGenerator generates unique identifiers.
type IDGenerator struct {
	*base.BaseTool
}

// NewIDGenerator creates a new IDGenerator tool.
func NewIDGenerator() *IDGenerator {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (generate_uuid, generate_short_id)",
				Enum:        []interface{}{"generate_uuid", "generate_short_id"},
			},
			"count": {
				Type:        "integer",
				Description: "Number of IDs to generate (default: 1)",
				Default:     1,
			},
		},
		Required: []string{"operation"},
	}

	return &IDGenerator{
		BaseTool: base.NewBaseToolWithCapabilities("id_generator", "Generate unique identifiers (UUID or short ID)", core.CategorySystem, []core.Capability{core.CapabilityExternal}, params),
	}
}

// Execute performs the ID generation operation.
func (t *IDGenerator) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	count := getInt(params, "count", 1)
	if count <= 0 {
		count = 1
	}
	if count > 100 {
		return core.NewErrorResult("count cannot exceed 100"), nil
	}

	switch operation {
	case "generate_uuid":
		return t.generateUUID(ctx, count)
	case "generate_short_id":
		return t.generateShortID(ctx, count)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// generateUUID generates one or more UUIDs.
func (t *IDGenerator) generateUUID(ctx context.Context, count int) (core.Result, error) {
	ids := make([]string, count)
	for i := 0; i < count; i++ {
		id := uuid.New()
		ids[i] = id.String()
	}

	result := map[string]interface{}{
		"operation": "generate_uuid",
		"count":     count,
		"ids":       ids,
	}

	if count == 1 {
		result["id"] = ids[0]
	}

	return core.NewResult(true, result), nil
}

// generateShortID generates one or more short IDs.
func (t *IDGenerator) generateShortID(ctx context.Context, count int) (core.Result, error) {
	ids := make([]string, count)
	for i := 0; i < count; i++ {
		// Generate a short ID from first 8 characters of UUID
		id := uuid.New()
		shortID := id.String()[:8]
		ids[i] = shortID
	}

	result := map[string]interface{}{
		"operation": "generate_short_id",
		"count":     count,
		"ids":       ids,
	}

	if count == 1 {
		result["id"] = ids[0]
	}

	return core.NewResult(true, result), nil
}

func (t *IDGenerator) IsIdempotent() bool { return true }

// getInt safely gets an int parameter.
func getInt(params map[string]interface{}, key string, defaultVal int) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}
