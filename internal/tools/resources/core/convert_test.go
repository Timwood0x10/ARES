package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParameterSchemaToMap_EmptyTypeIsValid pins the shape a provider validates.
//
// OpenAI-compatible providers validate the tool LIST as JSON Schema: one tool whose parameters carry
// `"type": ""` fails the whole request with a 400, so a single incompletely-declared tool takes down
// every LLM call rather than just that tool. Found by running a real model against a schema whose
// root type was unset.
func TestParameterSchemaToMap_EmptyTypeIsValid(t *testing.T) {
	t.Run("root type defaults to object", func(t *testing.T) {
		got := ParameterSchemaToMap(&ParameterSchema{
			Properties: map[string]*Parameter{"query": {Type: "string"}},
			Required:   []string{"query"},
		})
		assert.Equal(t, "object", got["type"])
	})

	t.Run("declared root type is preserved", func(t *testing.T) {
		got := ParameterSchemaToMap(&ParameterSchema{Type: "object"})
		assert.Equal(t, "object", got["type"])
	})

	t.Run("a property without a type is omitted, not empty", func(t *testing.T) {
		got := ParameterSchemaToMap(&ParameterSchema{
			Type:       "object",
			Properties: map[string]*Parameter{"opaque": {Description: "no type declared"}},
		})
		props, ok := got["properties"].(map[string]interface{})
		require.True(t, ok)
		prop, ok := props["opaque"].(map[string]interface{})
		require.True(t, ok)
		_, present := prop["type"]
		assert.False(t, present, "an empty type string is what the metaschema rejects")

		// And the encoded form really contains no empty type.
		encoded, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), `"type":""`)
	})

	t.Run("nil schema stays nil", func(t *testing.T) {
		assert.Nil(t, ParameterSchemaToMap(nil))
	})
}
