//go:build integration
// +build integration

// Package repositories provides tenant isolation e2e tests.
//
// These tests verify the explicit WHERE tenant_id filtering introduced by the
// RLS fix (stage 3): two tenants write identical content and must never see
// each other's rows, and cross-tenant deletes must fail with not-found.
package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/errors"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// TestTenantIsolation_KnowledgeSearch verifies that two tenants writing the
// same knowledge content never see each other's rows through either vector or
// keyword search, and that a cross-tenant delete is rejected.
func TestTenantIsolation_KnowledgeSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewKnowledgeRepository(db, db)
	ctx := context.Background()

	sharedContent := "shared knowledge content for isolation check"
	embedding := createTestEmbedding()

	mkChunk := func(tenantID string) *storage_models.KnowledgeChunk {
		return &storage_models.KnowledgeChunk{
			TenantID:         tenantID,
			Content:          sharedContent,
			Embedding:        embedding,
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
			SourceType:       "test",
			Source:           "tenant-isolation",
			Metadata:         map[string]interface{}{"purpose": "isolation"},
		}
	}

	chunkA := mkChunk("tenant-A")
	require.NoError(t, repo.Create(ctx, chunkA))
	chunkB := mkChunk("tenant-B")
	require.NoError(t, repo.Create(ctx, chunkB))

	// Keyword search: tenant A must only see its own row even though B wrote
	// the identical content.
	byKeywordA, err := repo.SearchByKeyword(ctx, sharedContent, "tenant-A", 10)
	require.NoError(t, err)
	require.Len(t, byKeywordA, 1, "tenant A keyword search must not leak tenant B rows")
	assert.Equal(t, "tenant-A", byKeywordA[0].TenantID)

	byKeywordB, err := repo.SearchByKeyword(ctx, sharedContent, "tenant-B", 10)
	require.NoError(t, err)
	require.Len(t, byKeywordB, 1, "tenant B keyword search must not leak tenant A rows")
	assert.Equal(t, "tenant-B", byKeywordB[0].TenantID)

	// Vector search: same isolation requirement.
	byVectorA, err := repo.SearchByVector(ctx, embedding, "tenant-A", 10)
	require.NoError(t, err)
	for _, r := range byVectorA {
		assert.Equal(t, "tenant-A", r.TenantID, "tenant A vector search leaked a foreign row")
	}
	byVectorB, err := repo.SearchByVector(ctx, embedding, "tenant-B", 10)
	require.NoError(t, err)
	for _, r := range byVectorB {
		assert.Equal(t, "tenant-B", r.TenantID, "tenant B vector search leaked a foreign row")
	}

	// Cross-tenant delete must fail: tenant B cannot delete tenant A's chunk.
	err = repo.Delete(ctx, chunkA.ID, "tenant-B")
	require.ErrorIs(t, err, errors.ErrRecordNotFound, "cross-tenant delete must be rejected")

	// Same-tenant delete must succeed.
	require.NoError(t, repo.Delete(ctx, chunkA.ID, "tenant-A"))
}

// TestTenantIsolation_ExperienceSearch verifies tenant isolation on the
// experience repository for both search paths and delete.
func TestTenantIsolation_ExperienceSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewExperienceRepository(db)
	ctx := context.Background()

	sharedInput := "shared experience input for isolation check"
	embedding := createTestEmbedding()

	mkExp := func(tenantID string) *storage_models.Experience {
		return &storage_models.Experience{
			TenantID:         tenantID,
			Type:             storage_models.ExperienceTypeQuery,
			Input:            sharedInput,
			Output:           "output",
			Embedding:        embedding,
			EmbeddingModel:   "e5-large",
			EmbeddingVersion: 1,
			Score:            0.8,
			Success:          true,
			AgentID:          "agent-1",
			Metadata:         map[string]interface{}{"purpose": "isolation"},
			DecayAt:          time.Now().Add(30 * 24 * time.Hour),
			CreatedAt:        time.Now(),
		}
	}

	expA := mkExp("tenant-A")
	require.NoError(t, repo.Create(ctx, expA))
	expB := mkExp("tenant-B")
	require.NoError(t, repo.Create(ctx, expB))

	byKeywordA, err := repo.SearchByKeyword(ctx, sharedInput, "tenant-A", 10)
	require.NoError(t, err)
	require.Len(t, byKeywordA, 1, "tenant A experience keyword search leaked a foreign row")
	assert.Equal(t, "tenant-A", byKeywordA[0].TenantID)

	byVectorB, err := repo.SearchByVector(ctx, embedding, "tenant-B", 10)
	require.NoError(t, err)
	for _, r := range byVectorB {
		assert.Equal(t, "tenant-B", r.TenantID, "tenant B experience vector search leaked a foreign row")
	}

	err = repo.Delete(ctx, expA.ID, "tenant-B")
	require.ErrorIs(t, err, errors.ErrRecordNotFound, "cross-tenant experience delete must be rejected")
	require.NoError(t, repo.Delete(ctx, expA.ID, "tenant-A"))
}

// TestTenantIsolation_ToolDelete verifies cross-tenant deletes are rejected on
// the tool repository (vector search path is exercised for completeness).
func TestTenantIsolation_ToolDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewToolRepository(db)
	ctx := context.Background()

	embedding := createTestEmbedding()
	toolA := &storage_models.Tool{
		TenantID:         "tenant-A",
		Name:             "isolated-tool",
		Description:      "tenant isolation tool",
		Embedding:        embedding,
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		AgentType:        "test-agent",
		Tags:             []string{"test"},
		UsageCount:       0,
		SuccessRate:      0.0,
		Metadata:         map[string]interface{}{"purpose": "isolation"},
		CreatedAt:        time.Now(),
	}
	require.NoError(t, repo.Create(ctx, toolA))

	toolB := *toolA
	toolB.TenantID = "tenant-B"
	toolB.Name = "isolated-tool-b"
	require.NoError(t, repo.Create(ctx, &toolB))

	// Vector search from tenant B must not return tenant A's tool.
	byVectorB, err := repo.SearchByVector(ctx, embedding, "tenant-B", 10)
	require.NoError(t, err)
	for _, r := range byVectorB {
		assert.Equal(t, "tenant-B", r.TenantID, "tenant B tool vector search leaked a foreign row")
	}

	// Cross-tenant delete rejected; same-tenant delete succeeds.
	err = repo.Delete(ctx, toolA.ID, "tenant-B")
	require.ErrorIs(t, err, errors.ErrRecordNotFound, "cross-tenant tool delete must be rejected")
	require.NoError(t, repo.Delete(ctx, toolA.ID, "tenant-A"))
}

// TestTenantIsolation_ConversationDelete verifies the conversation delete
// path enforces tenant ownership.
func TestTenantIsolation_ConversationDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewConversationRepository(db)
	ctx := context.Background()

	conv := &storage_models.Conversation{
		SessionID: "session-isolation-1",
		TenantID:  "tenant-A",
		UserID:    "user-1",
		AgentID:   "agent-1",
		Role:      "user",
		Content:   "isolated conversation",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}
	require.NoError(t, repo.Create(ctx, conv))

	err := repo.Delete(ctx, conv.ID, "tenant-B")
	require.ErrorIs(t, err, errors.ErrRecordNotFound, "cross-tenant conversation delete must be rejected")
	require.NoError(t, repo.Delete(ctx, conv.ID, "tenant-A"))
}
