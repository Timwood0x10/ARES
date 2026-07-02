package storage

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSearchResultZeroValue(t *testing.T) {
	var sr SearchResult
	assert.Empty(t, sr.ID, "zero-value ID should be empty")
	assert.Equal(t, 0.0, sr.Score, "zero-value Score should be 0.0")
	assert.Nil(t, sr.Metadata, "zero-value Metadata should be nil")
}

func TestSearchResultConstruction(t *testing.T) {
	sr := SearchResult{
		ID:    "doc-42",
		Score: 0.95,
		Metadata: map[string]any{
			"source": "test",
			"rank":   1,
		},
	}
	assert.Equal(t, "doc-42", sr.ID)
	assert.Equal(t, 0.95, sr.Score)
	assert.Equal(t, "test", sr.Metadata["source"])
	assert.Equal(t, 1, sr.Metadata["rank"])
}

func TestSearchResultJSONTags(t *testing.T) {
	typ := reflect.TypeOf(SearchResult{})

	idField, _ := typ.FieldByName("ID")
	assert.Equal(t, "id", idField.Tag.Get("json"), "ID should serialize to 'id'")

	scoreField, _ := typ.FieldByName("Score")
	assert.Equal(t, "score", scoreField.Tag.Get("json"), "Score should serialize to 'score'")

	metaField, _ := typ.FieldByName("Metadata")
	assert.Equal(t, "metadata,omitempty", metaField.Tag.Get("json"), "Metadata should serialize to 'metadata,omitempty'")
}

func TestVectorStoreInterfaceHasExpectedMethods(t *testing.T) {
	typ := reflect.TypeOf((*VectorStore)(nil)).Elem()

	_, ok := typ.MethodByName("Search")
	assert.True(t, ok, "VectorStore should have Search method")

	_, ok = typ.MethodByName("AddEmbedding")
	assert.True(t, ok, "VectorStore should have AddEmbedding method")

	_, ok = typ.MethodByName("CreateCollection")
	assert.True(t, ok, "VectorStore should have CreateCollection method")

	assert.Equal(t, 3, typ.NumMethod(), "VectorStore interface should have exactly 3 methods")
}
