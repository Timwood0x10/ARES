package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/google/uuid"

	memory "github.com/Timwood0x10/ares/api/ares_memory"
	internalMemory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
	"github.com/Timwood0x10/ares/internal/storage/postgres/services"

	"gopkg.in/yaml.v3"
)

// SearchResult simple search result (local type for backward compatibility)
type SearchResult struct {
	ID      string
	IsKB    bool
	Content string
	Source  string
	Score   float64
}

// UserProfile unified user profile structure
type UserProfile struct {
	UserID      string
	Name        string
	Profession  string
	Skills      []string
	Interests   []string
	Bio         string
	LastUpdated time.Time
	Confidence  float64 // How confident we are about this profile data
}

// UserProfileService manages user profiles
type UserProfileService struct {
	distilledRepo *repositories.DistilledMemoryRepository
	embedding     *embedding.EmbeddingClient
	llmClient     *llm.Client
}

// NewUserProfileService creates a new user profile service
func NewUserProfileService(distilledRepo *repositories.DistilledMemoryRepository, embedding *embedding.EmbeddingClient, llmClient *llm.Client) *UserProfileService {
	return &UserProfileService{
		distilledRepo: distilledRepo,
		embedding:     embedding,
		llmClient:     llmClient,
	}
}

// ExtractProfileFromSelfIntro extracts profile from self-introduction
func (s *UserProfileService) ExtractProfileFromSelfIntro(ctx context.Context, tenantID, userID, selfIntro string) (*UserProfile, error) {
	log.Printf("👤 [Profile] Extracting profile from self-introduction for user: %s", userID)

	profile := &UserProfile{
		UserID:      userID,
		LastUpdated: time.Now(),
		Confidence:  0.8, // Default confidence for self-introduction
	}

	// Extract information from self-introduction
	lowerIntro := strings.ToLower(selfIntro)

	// Extract name (already done by extractUserID, but refine here)
	profile.Name = cases.Title(language.English).String(userID)

	// Extract profession
	professionKeywords := []string{"programmer", "developer", "engineer", "designer", "manager", "student", "researcher"}
	for _, keyword := range professionKeywords {
		if strings.Contains(lowerIntro, keyword) {
			profile.Profession = cases.Title(language.English).String(keyword)
			break
		}
	}

	// Extract skills
	skillKeywords := []string{"javascript", "typescript", "js", "ts", "vue", "react", "angular", "go", "golang", "python", "java", "rust"}
	for _, keyword := range skillKeywords {
		if strings.Contains(lowerIntro, keyword) {
			profile.Skills = append(profile.Skills, strings.ToUpper(keyword))
		}
	}

	// Use LLM to extract more detailed profile if available
	if s.llmClient != nil {
		prompt := fmt.Sprintf(`Extract structured user profile information from this self-introduction:

Self-introduction: "%s"

Extract and return ONLY the following fields in JSON format:
{
  "name": "user's name",
  "profession": "user's profession",
  "skills": ["skill1", "skill2"],
  "interests": ["interest1", "interest2"],
  "bio": "short bio"
}

If a field cannot be extracted, use null or empty array.`, selfIntro)

		genCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		response, err := s.llmClient.Generate(genCtx, prompt)
		cancel()

		if err == nil {
			log.Printf("👤 [Profile] LLM extracted profile data: %s", truncateString(response, 100))
			// Parse JSON response and update profile
			var extractedProfile struct {
				Name       string   `json:"name"`
				Profession string   `json:"profession"`
				Skills     []string `json:"skills"`
				Interests  []string `json:"interests"`
				Bio        string   `json:"bio"`
			}

			// Clean the response - remove markdown code blocks if present
			cleanResponse := strings.TrimSpace(response)
			if strings.HasPrefix(cleanResponse, "```json") {
				cleanResponse = strings.TrimPrefix(cleanResponse, "```json")
				cleanResponse = strings.TrimSuffix(cleanResponse, "```")
				cleanResponse = strings.TrimSpace(cleanResponse)
			} else if strings.HasPrefix(cleanResponse, "```") {
				cleanResponse = strings.TrimPrefix(cleanResponse, "```")
				cleanResponse = strings.TrimSuffix(cleanResponse, "```")
				cleanResponse = strings.TrimSpace(cleanResponse)
			}

			// Parse JSON
			if err := json.Unmarshal([]byte(cleanResponse), &extractedProfile); err == nil {
				// Update profile with extracted data
				if extractedProfile.Name != "" {
					profile.Name = cases.Title(language.English).String(extractedProfile.Name)
				}
				if extractedProfile.Profession != "" {
					profile.Profession = cases.Title(language.English).String(extractedProfile.Profession)
				}
				if len(extractedProfile.Skills) > 0 {
					profile.Skills = extractedProfile.Skills
				}
				if len(extractedProfile.Interests) > 0 {
					profile.Interests = extractedProfile.Interests
				}
				if extractedProfile.Bio != "" {
					profile.Bio = extractedProfile.Bio
				}
				profile.Confidence = 0.8 // Set confidence for LLM-extracted data
				log.Printf("✅ [Profile] Successfully parsed and updated profile for user: %s", userID)
			} else {
				log.Printf("⚠️  [Profile] Failed to parse JSON response: %v", err)
			}
		}
	}

	return profile, nil
}

// StoreProfile stores profile as a distilled memory
func (s *UserProfileService) StoreProfile(ctx context.Context, tenantID string, profile *UserProfile) error {
	if profile == nil {
		return fmt.Errorf("profile is nil")
	}

	log.Printf("👤 [Profile] Storing profile for user: %s", profile.UserID)

	// Build profile content
	var content strings.Builder
	content.WriteString("User Profile:\n")
	fmt.Fprintf(&content, "Name: %s\n", profile.Name)
	if profile.Profession != "" {
		fmt.Fprintf(&content, "Profession: %s\n", profile.Profession)
	}
	if len(profile.Skills) > 0 {
		fmt.Fprintf(&content, "Skills: %s\n", strings.Join(profile.Skills, ", "))
	}
	if len(profile.Interests) > 0 {
		fmt.Fprintf(&content, "Interests: %s\n", strings.Join(profile.Interests, ", "))
	}
	if profile.Bio != "" {
		fmt.Fprintf(&content, "Bio: %s\n", profile.Bio)
	}

	// Generate embedding
	var embeddingVec []float64
	var err error
	if s.embedding != nil {
		embeddingVec, err = s.embedding.EmbedWithPrefix(ctx, content.String(), "profile:")
		if err != nil {
			log.Printf("👤 [Profile] Failed to generate embedding: %v", err)
			return fmt.Errorf("generate embedding: %w", err)
		}
	}

	// Store as distilled memory
	distilledMem := &repositories.DistilledMemory{
		ID:               uuid.New().String(),
		TenantID:         tenantID,
		UserID:           profile.UserID,
		SessionID:        "",
		Content:          content.String(),
		Embedding:        embeddingVec,
		EmbeddingModel:   s.embedding.GetModel(),
		EmbeddingVersion: 1,
		MemoryType:       "profile",
		Importance:       profile.Confidence,
		Metadata: map[string]interface{}{
			"profile_type": "unified",
			"name":         profile.Name,
			"profession":   profile.Profession,
			"skills":       profile.Skills,
			"interests":    profile.Interests,
			"confidence":   profile.Confidence,
			"last_updated": profile.LastUpdated,
		},
		AccessCount:    0,
		LastAccessedAt: nil,
		ExpiresAt:      time.Now().Add(90 * 24 * time.Hour),
		CreatedAt:      time.Now(),
	}

	return s.distilledRepo.Create(ctx, distilledMem)
}

// GetProfile retrieves user profile from distilled memories
func (s *UserProfileService) GetProfile(ctx context.Context, tenantID, userID string) (*UserProfile, error) {
	log.Printf("👤 [Profile] Retrieving profile for user: %s", userID)

	memories, err := s.distilledRepo.GetByUserID(ctx, tenantID, userID, 5)
	if err != nil {
		return nil, fmt.Errorf("get memories: %w", err)
	}

	// Find the most recent profile memory
	var latestProfileMem *repositories.DistilledMemory
	var latestTime time.Time

	for _, mem := range memories {
		if mem.MemoryType == "profile" && mem.CreatedAt.After(latestTime) {
			latestTime = mem.CreatedAt
			latestProfileMem = mem
		}
	}

	if latestProfileMem == nil {
		return nil, fmt.Errorf("profile not found for user: %s", userID)
	}

	// Parse profile from memory content
	profile := &UserProfile{
		UserID:      userID,
		LastUpdated: latestProfileMem.CreatedAt,
		Confidence:  latestProfileMem.Importance,
	}

	// Parse content
	lines := strings.Split(latestProfileMem.Content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Name:") {
			profile.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		} else if strings.HasPrefix(line, "Profession:") {
			profile.Profession = strings.TrimSpace(strings.TrimPrefix(line, "Profession:"))
		} else if strings.HasPrefix(line, "Skills:") {
			skillsStr := strings.TrimSpace(strings.TrimPrefix(line, "Skills:"))
			if skillsStr != "" {
				profile.Skills = strings.Split(skillsStr, ", ")
			}
		} else if strings.HasPrefix(line, "Interests:") {
			interestsStr := strings.TrimSpace(strings.TrimPrefix(line, "Interests:"))
			if interestsStr != "" {
				profile.Interests = strings.Split(interestsStr, ", ")
			}
		} else if strings.HasPrefix(line, "Bio:") {
			profile.Bio = strings.TrimSpace(strings.TrimPrefix(line, "Bio:"))
		}
	}

	return profile, nil
}

// Config configuration structure
type Config struct {
	Database struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		Database string `yaml:"database"`
	} `yaml:"database"`

	EmbeddingServiceURL string `yaml:"embedding_service_url"`
	EmbeddingModel      string `yaml:"embedding_model"`

	LLM struct {
		Provider  string `yaml:"provider"`
		APIKey    string `yaml:"api_key"`
		BaseURL   string `yaml:"base_url"`
		Model     string `yaml:"model"`
		Timeout   int    `yaml:"timeout"`
		MaxTokens int    `yaml:"max_tokens"`
	} `yaml:"llm"`

	Memory struct {
		Enabled               bool `yaml:"enabled"`
		MaxHistory            int  `yaml:"max_history"`
		MaxSessions           int  `yaml:"max_sessions"`
		EnableDistillation    bool `yaml:"enable_distillation"`
		DistillationThreshold int  `yaml:"distillation_threshold"`
	} `yaml:"memory"`

	Knowledge struct {
		ChunkSize    int     `yaml:"chunk_size"`
		ChunkOverlap int     `yaml:"chunk_overlap"`
		TopK         int     `yaml:"top_k"`
		MinScore     float64 `yaml:"min_score"`
	} `yaml:"knowledge"`
}

// Chunk document chunk
type Chunk struct {
	Index int
	Text  string
	Hash  string
}

// KnowledgeBase simplified knowledge base interface
type KnowledgeBase struct {
	config          *Config
	pool            *postgres.Pool
	repo            *repositories.KnowledgeRepository
	distilledRepo   *repositories.DistilledMemoryRepository
	embedding       *embedding.EmbeddingClient
	llmClient       *llm.Client
	retrieval       *services.SimpleRetrievalService
	memMgr          internalMemory.MemoryManager
	distillationSvc *memory.DistillationServiceImpl
	profileService  *UserProfileService
	sessionID       string
	messageCount    int
	distilledRounds int // Track how many rounds have been distilled
}

func main() {
	// Command line arguments
	saveFlag := flag.String("save", "", "Path to document to save")
	chatFlag := flag.Bool("chat", false, "Enable chat mode")
	listFlag := flag.Bool("list", false, "List all documents")
	deleteFlag := flag.String("delete", "", "Document ID to delete")
	tenantFlag := flag.String("tenant", "default", "Tenant ID")
	configFlag := flag.String("config", "config.yaml", "Config file path")

	flag.Parse()

	// Load configuration
	config, err := loadConfig(*configFlag)
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// Create knowledge base
	kb, err := NewKnowledgeBase(config)
	if err != nil {
		slog.Error("Failed to create knowledge base", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := kb.Close(); err != nil {
			slog.Error("Failed to close knowledge base", "error", err)
			os.Exit(1)
		}
	}()

	ctx := context.Background()

	// Handle different modes
	switch {
	case *saveFlag != "":
		// Import document mode - set 5 minute timeout
		importCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		log.Printf("Importing document: %s", *saveFlag)
		docID, err := kb.ImportDocuments(importCtx, *tenantFlag, *saveFlag)
		cancel()

		if err != nil {
			if err == context.DeadlineExceeded {
				slog.Error("Import timeout (5 minutes exceeded)")
				os.Exit(1)
			}
			slog.Error("Failed to import document", "error", err)
			os.Exit(1)
		}
		log.Printf("Document imported successfully. Document ID: %s", docID)

	case *chatFlag:
		// Q&A mode
		log.Println("Chat mode. Enter your questions (type 'exit' to quit):")
		kb.StartChat(ctx, *tenantFlag)

	case *listFlag:
		// List all documents - set 30 second timeout
		listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		docs, err := kb.ListDocuments(listCtx, *tenantFlag)
		cancel()

		if err != nil {
			if err == context.DeadlineExceeded {
				slog.Error("List timeout")
				os.Exit(1)
			}
			slog.Error("Failed to list documents", "error", err)
			os.Exit(1)
		}
		if len(docs) == 0 {
			log.Println("No documents found")
		} else {
			log.Println("Documents:")
			for _, doc := range docs {
				log.Printf("  - ID: %s, Source: %s, Chunks: %d", doc.ID, doc.Source, doc.ChunkCount)
			}
		}

	case *deleteFlag != "":
		// Delete document - set 30 second timeout
		deleteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		log.Printf("Deleting document: %s", *deleteFlag)
		if err := kb.DeleteDocument(deleteCtx, *tenantFlag, *deleteFlag); err != nil {
			cancel()
			if err == context.DeadlineExceeded {
				slog.Error("Delete timeout")
				os.Exit(1)
			}
			slog.Error("Failed to delete document", "error", err)
			os.Exit(1)
		}
		cancel()
		log.Printf("Document deleted successfully")

	default:
		printUsage()
	}
}

// NewKnowledgeBase create knowledge base instance
func NewKnowledgeBase(config *Config) (*KnowledgeBase, error) {
	// Create database configuration
	dbConfig := &postgres.Config{
		Host:            config.Database.Host,
		Port:            config.Database.Port,
		User:            config.Database.User,
		Password:        config.Database.Password,
		Database:        config.Database.Database,
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
		Embedding:       postgres.DefaultEmbeddingConfig(),
	}

	// Create database connection pool
	pool, err := postgres.NewPool(dbConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}

	// Create embedding service client
	embeddingClient := embedding.NewEmbeddingClient(
		config.EmbeddingServiceURL,
		config.EmbeddingModel,
		nil,
		30*time.Second,
	)

	// Create knowledge repository
	kbRepo := repositories.NewKnowledgeRepository(pool.GetDB(), pool.GetDB())

	// Create distilled memory repository
	distilledRepo := repositories.NewDistilledMemoryRepository(pool.GetDB(), pool.GetDB())

	// Create simple retrieval service (pure vector similarity, no complex weights)
	retrievalService := services.NewSimpleRetrievalService(
		kbRepo,
		embeddingClient,
		&services.SimpleRetrievalConfig{
			TopK:        config.Knowledge.TopK,
			MinScore:    config.Knowledge.MinScore,
			QueryPrefix: "query:",
		},
	)

	// Create LLM client for RAG (optional, may be nil)
	var llmClient *llm.Client
	if config.LLM.Provider != "" && config.LLM.Model != "" {
		llmConfig := &llm.Config{
			Provider: config.LLM.Provider,
			APIKey:   config.LLM.APIKey,
			BaseURL:  config.LLM.BaseURL,
			Model:    config.LLM.Model,
			Timeout:  config.LLM.Timeout,
		}
		var err error
		llmClient, err = llm.NewClient(llmConfig)
		if err != nil {
			log.Printf("Failed to create LLM client: %v, RAG will be disabled", err)
			llmClient = nil
		}
	}

	// Create memory manager for conversation history
	var memMgr internalMemory.MemoryManager
	if config.Memory.Enabled {
		memConfig := &internalMemory.MemoryConfig{
			Enabled:        true,
			Storage:        "memory",
			MaxHistory:     config.Memory.MaxHistory,
			MaxSessions:    config.Memory.MaxSessions,
			SessionTTL:     24 * time.Hour,
			TaskTTL:        7 * 24 * time.Hour,
			VectorDim:      128,
			EnablePostgres: false,
		}
		var err error
		memMgr, err = internalMemory.NewMemoryManager(memConfig)
		if err != nil {
			slog.Warn("Failed to create memory manager", "error", err)
			memMgr = nil
		} else {
			slog.Info("Memory manager created successfully")
		}
	}

	// Create distillation service if memory is enabled and distillation is configured
	var distillationSvc *memory.DistillationServiceImpl
	if config.Memory.Enabled && config.Memory.EnableDistillation {
		// Create experience repository adapter
		expRepo := NewExperienceRepositoryAdapter(distilledRepo)

		// Create distillation configuration
		distillConfig := &memory.DistillationConfig{
			MinImportance:              0.6,
			ConflictThreshold:          0.85,
			MaxMemoriesPerDistillation: 3,
			MaxSolutionsPerTenant:      5000,
			EnableCodeFilter:           true,
			EnableStacktraceFilter:     true,
			EnableLogFilter:            true,
			EnableMarkdownTableFilter:  true,
			EnableCrossTurnExtraction:  true,
			EnableLengthBonus:          true,
			LengthThreshold:            60,
			LengthBonus:                0.1,
			TopNBeforeConflict:         true,
			ConflictSearchLimit:        5,
			PrecisionOverRecall:        true,
		}

		var err error
		distillationSvc, err = memory.NewDistillationServiceWithEmbedder(distillConfig, embeddingClient, expRepo)
		if err != nil {
			slog.Warn("Failed to create distillation service", "error", err)
			distillationSvc = nil
		} else {
			slog.Info("Distillation service created successfully")
		}
	}

	// Create profile service for unified user profile management
	profileService := NewUserProfileService(distilledRepo, embeddingClient, llmClient)

	return &KnowledgeBase{
		config:          config,
		pool:            pool,
		repo:            kbRepo,
		distilledRepo:   distilledRepo,
		embedding:       embeddingClient,
		llmClient:       llmClient,
		retrieval:       retrievalService,
		memMgr:          memMgr,
		distillationSvc: distillationSvc,
		profileService:  profileService,
		sessionID:       "",
		messageCount:    0,
	}, nil
}

// ImportDocuments import documents to knowledge base
func (kb *KnowledgeBase) ImportDocuments(ctx context.Context, tenantID, docPath string) (string, error) {
	// Read document content
	content, err := os.ReadFile(docPath)
	if err != nil {
		return "", fmt.Errorf("read document: %w", err)
	}

	// Generate document ID
	docID := uuid.New().String()

	// Chunk processing
	chunks := kb.chunkDocument(string(content), kb.config.Knowledge.ChunkSize, kb.config.Knowledge.ChunkOverlap)
	log.Printf("Document split into %d chunks", len(chunks))

	// Batch process chunks
	successCount := 0
	for i, chunk := range chunks {
		log.Printf("Processing chunk %d/%d...", i+1, len(chunks))

		// Generate embedding vector (with timeout)
		chunkCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		embedding, err := kb.embedding.EmbedWithPrefix(chunkCtx, chunk.Text, "passage:")
		cancel()

		if err != nil {
			log.Printf("Failed to embed chunk %d: %v (skipping)", i, err)
			continue
		}

		// IMPORTANT: Normalize embedding vector for pgvector cosine distance
		// pgvector's <=> operator requires normalized vectors for accurate cosine distance
		embedding = postgres.NormalizeVector(embedding)

		// Store to database
		knowledgeChunk := &storage_models.KnowledgeChunk{
			TenantID:         tenantID,
			Content:          chunk.Text,
			Embedding:        embedding,
			EmbeddingModel:   kb.config.EmbeddingModel,
			EmbeddingVersion: 1,
			EmbeddingStatus:  "completed",
			SourceType:       "document",
			Source:           docPath,
			DocumentID:       docID,
			ChunkIndex:       chunk.Index,
			ContentHash:      chunk.Hash,
			AccessCount:      0,
		}

		if err := kb.repo.Create(ctx, knowledgeChunk); err != nil {
			log.Printf("Failed to save chunk %d: %v", i, err)
			continue
		}

		successCount++
	}

	log.Printf("Successfully imported %d/%d chunks", successCount, len(chunks))

	if successCount == 0 {
		return "", fmt.Errorf("failed to import any chunks")
	}

	return docID, nil
}

// Search retrieve knowledge base using simple vector similarity
func (kb *KnowledgeBase) Search(ctx context.Context, tenantID, question string) ([]*SearchResult, error) {
	log.Printf("Searching for: %s (tenant: %s)", question, tenantID)

	// Generate query embedding once, reuse for both searches
	queryEmbedding, embedErr := kb.embedding.EmbedWithPrefix(ctx, question, "query:")
	if embedErr != nil {
		log.Printf("Embedding failed: %v", embedErr)
	}

	// Search knowledge base chunks via SimpleRetrievalService
	results, err := kb.retrieval.Search(ctx, tenantID, question)
	if err != nil {
		log.Printf("Search error: %v", err)
		return nil, err
	}

	log.Printf("Search returned %d results from knowledge base", len(results))

	// Get KB chunk IDs
	var kbChunkIDs map[int]string
	if queryEmbedding != nil && kb.repo != nil {
		chunks, chunkErr := kb.repo.SearchByVector(ctx, queryEmbedding, tenantID, len(results))
		if chunkErr == nil {
			kbChunkIDs = make(map[int]string)
			for i, c := range chunks {
				kbChunkIDs[i] = c.ID
			}
		}
	}

	// Also search distilled memories for user corrections
	var distilledResults []*SearchResult
	if kb.distilledRepo != nil && queryEmbedding != nil {
		memories, searchErr := kb.distilledRepo.SearchByVector(ctx, queryEmbedding, tenantID, 5)
		if searchErr == nil {
			for _, mem := range memories {
				distilledResults = append(distilledResults, &SearchResult{
					ID:      mem.ID,
					IsKB:    false,
					Content: mem.Content,
					Source:  fmt.Sprintf("Memory (Type: %s, Importance: %.2f)", mem.MemoryType, mem.Importance),
					Score:   mem.Importance + 0.1,
				})
			}
			log.Printf("Found %d results from distilled memories", len(distilledResults))
		}
	}

	var localResults []*SearchResult
	for i, r := range results {
		id := ""
		if kbChunkIDs != nil {
			id = kbChunkIDs[i]
		}
		localResults = append(localResults, &SearchResult{
			ID:      id,
			IsKB:    true,
			Content: r.Content,
			Source:  r.Source,
			Score:   r.Score,
		})
	}
	localResults = append(localResults, distilledResults...)

	return localResults, nil
}

// GenerateAnswer generates a response based on retrieved documents using LLM.
// This implements the complete RAG pipeline: retrieve → generate → validate.
// Args:
// ctx - operation context.
// tenantID - tenant identifier for isolation.
// question - user question.
// Returns generated answer or error if generation fails.
func (kb *KnowledgeBase) GenerateAnswer(ctx context.Context, tenantID, question string) (string, error) {
	// Step 0: Add user question to memory
	if kb.memMgr != nil && kb.sessionID != "" {
		_ = kb.memMgr.AddMessage(ctx, kb.sessionID, "user", question)
	}

	// Step 0.5: Detect user intent
	lowerQuestion := strings.ToLower(question)
	isCorrection := strings.Contains(lowerQuestion, "纠正") ||
		strings.Contains(lowerQuestion, "纠错") ||
		strings.Contains(lowerQuestion, "改正") ||
		strings.Contains(lowerQuestion, "修正") ||
		strings.Contains(lowerQuestion, "不对") ||
		strings.Contains(lowerQuestion, "不是") ||
		// English correction keywords
		strings.Contains(lowerQuestion, "correct") ||
		strings.Contains(lowerQuestion, "fix") ||
		strings.Contains(lowerQuestion, "wrong") ||
		strings.Contains(lowerQuestion, "not right") ||
		strings.Contains(lowerQuestion, "update") ||
		strings.Contains(lowerQuestion, "change") ||
		strings.Contains(lowerQuestion, "modify") ||
		strings.Contains(lowerQuestion, "that's wrong") ||
		strings.Contains(lowerQuestion, "that is wrong") ||
		strings.Contains(lowerQuestion, "actually") ||
		strings.Contains(lowerQuestion, "actually i am") ||
		strings.Contains(lowerQuestion, "no i am") ||
		strings.Contains(lowerQuestion, "my actual") ||
		strings.Contains(lowerQuestion, "i actually")

	// Detect self-introduction and extract user ID using unified logic
	userID := kb.extractUserID(question)

	log.Printf("🔍 User ID detection: extracted_user_id='%s' (from: '%s')", userID, question)

	// Handle self-introduction - extract and store user profile
	if userID != "" && kb.profileService != nil {
		log.Printf("👤 Detected self-introduction: user_id=%s", userID)

		// Extract profile from self-introduction
		profile, err := kb.profileService.ExtractProfileFromSelfIntro(ctx, tenantID, userID, question)
		if err != nil {
			log.Printf("⚠️  [Profile] Failed to extract profile: %v", err)
		} else {
			// Store the extracted profile
			if err := kb.profileService.StoreProfile(ctx, tenantID, profile); err != nil {
				log.Printf("⚠️  [Profile] Failed to store profile: %v", err)
			} else {
				log.Printf("✅ [Profile] Profile extracted and stored for user: %s", userID)
			}
		}

		// Load existing profile to provide personalized greeting
		existingProfile, err := kb.profileService.GetProfile(ctx, tenantID, userID)
		if err == nil && existingProfile != nil {
			log.Printf("📊 Loaded existing profile for user %s", userID)

			// Build personalized greeting based on profile
			var greeting strings.Builder
			fmt.Fprintf(&greeting, "Hello %s! Welcome back! 👋\n\n", cases.Title(language.English).String(userID))

			if existingProfile.Profession != "" {
				fmt.Fprintf(&greeting, "I remember you're a %s", existingProfile.Profession)
				if len(existingProfile.Skills) > 0 {
					fmt.Fprintf(&greeting, " with skills in %s", strings.Join(existingProfile.Skills, ", "))
				}
				greeting.WriteString(".\n\n")
			}

			greeting.WriteString("How can I help you today?")

			// Inject profile context into conversation
			if kb.memMgr != nil {
				profileContext := fmt.Sprintf("User Profile: %s - %s | Skills: %s",
					existingProfile.Name, existingProfile.Profession, strings.Join(existingProfile.Skills, ", "))
				_ = kb.memMgr.AddMessage(ctx, kb.sessionID, "system", profileContext)
			}

			return greeting.String(), nil
		}

		// Fallback: simple greeting if no profile found
		log.Printf("ℹ️ No existing profile found for user %s", userID)
		return fmt.Sprintf("Hello %s! Nice to meet you. I'll remember our conversation for future reference. Please tell me more about your background and technology stack.", cases.Title(language.English).String(userID)), nil
	}

	// Check for profile questions ("who am I", "what's my technology stack", etc.)
	profileKeywords := []string{
		"who am i", "what's my", "what is my", "my technology", "my stack", "my profile",
		"我是谁", "我的技术栈", "我的技术", "我的简历",
		// Extended keywords for better detection
		"who is", "remember", "recall", "do you know", "do you remember",
		"是谁", "记得", "回忆", "认识",
	}
	isProfileQuestion := false
	for _, keyword := range profileKeywords {
		if strings.Contains(lowerQuestion, keyword) {
			isProfileQuestion = true
			break
		}
	}

	// Handle profile questions - search distilled memories for the specific user
	if isProfileQuestion && kb.distilledRepo != nil {
		log.Printf("👤 Detected profile question, searching user memories...")

		// Extract user ID from the profile question
		// For questions like "who is Ken?", we need to extract "Ken" as the user ID
		questionUserID := ""
		lowerQuestionForID := strings.ToLower(question)

		// Pattern matching for "who is [name]?"
		if strings.HasPrefix(lowerQuestionForID, "who is ") {
			namePart := strings.TrimSpace(lowerQuestionForID[len("who is "):])
			namePart = strings.TrimSuffix(namePart, "?")
			namePart = strings.TrimSpace(namePart)
			questionUserID = namePart
		} else if strings.Contains(lowerQuestionForID, "who am i") || strings.Contains(lowerQuestionForID, "我是谁") {
			// Pattern matching for "你是谁" / "我是谁" - use current user
			// Try to get user ID from session context
			questionUserID = ""
		}

		var memories []*repositories.DistilledMemory
		var err error

		// If we extracted a user ID from the question, query by user ID
		if questionUserID != "" {
			log.Printf("👤 Extracted user ID from profile question: '%s'", questionUserID)
			memories, err = kb.distilledRepo.GetByUserID(ctx, tenantID, questionUserID, 10)
			if err != nil {
				log.Printf("Failed to get memories for user '%s': %v", questionUserID, err)
			}
		}

		// If no memories found with user ID, try vector search but filter for profile type
		if len(memories) == 0 {
			log.Printf("👤 No memories found for user '%s', trying vector search with profile filter...", questionUserID)
			var queryEmbedding []float64
			if kb.embedding != nil {
				queryEmbedding, err = kb.embedding.EmbedWithPrefix(ctx, question, "query:")
				if err != nil {
					log.Printf("Failed to generate embedding for user memory search: %v", err)
				}
			}

			if len(queryEmbedding) > 0 {
				allMemories, err := kb.distilledRepo.SearchByVector(ctx, queryEmbedding, tenantID, 10)
				if err != nil {
					log.Printf("Vector search on user memories failed: %v", err)
				} else {
					// Filter for profile type memories only
					for _, mem := range allMemories {
						if mem.MemoryType == "profile" {
							memories = append(memories, mem)
						}
					}
					log.Printf("👤 Filtered %d profile memories from vector search", len(memories))
				}
			}
		}

		if len(memories) > 0 {
			log.Printf("📊 Found %d user memories for profile question", len(memories))

			// Deduplicate memories based on content
			seen := make(map[string]bool)
			var uniqueMemories []*repositories.DistilledMemory
			for _, mem := range memories {
				if !seen[mem.Content] {
					seen[mem.Content] = true
					uniqueMemories = append(uniqueMemories, mem)
				}
			}
			log.Printf("📊 Deduplicated to %d unique memories", len(uniqueMemories))

			// Build structured context for LLM
			var profileContext strings.Builder
			profileContext.WriteString("User Profile Information from conversation history:\n")
			profileContext.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

			for i, mem := range uniqueMemories {
				fmt.Fprintf(&profileContext, "[%d] %s\n", i+1, mem.Content)
			}

			// Add context to memory manager
			if kb.memMgr != nil {
				_ = kb.memMgr.AddMessage(ctx, kb.sessionID, "system", profileContext.String())
			}

			// Use LLM to generate natural, polished answer
			if kb.llmClient != nil {
				prompt := fmt.Sprintf(`You are a helpful assistant. Answer the user's question based on the user profile information provided.

User Question: %s

User Profile Information:
%s

IMPORTANT INSTRUCTIONS:
1. Synthesize the information naturally - don't just list the memories
2. Remove duplicates and merge related information
3. Present the answer in a friendly, conversational tone
4. Focus on the most important and relevant information
5. If the information is incomplete or outdated, mention that gracefully
6. Be concise but comprehensive

Answer:`, question, profileContext.String())

				genCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
				var genErr error
				userMemoryAnswer, genErr := kb.llmClient.Generate(genCtx, prompt)
				cancel()

				if genErr != nil {
					log.Printf("LLM generation failed for profile answer: %v, using fallback", genErr)
					// Fallback: simple formatted answer
					userMemoryAnswer = "Based on our conversation history, here's what I know:\n\n"
					for _, mem := range uniqueMemories {
						userMemoryAnswer += fmt.Sprintf("• %s\n", mem.Content)
					}
				}
				return userMemoryAnswer, nil
			} else {
				// Fallback if LLM not available
				userMemoryAnswer := "Based on our conversation history, here's what I know:\n\n"
				for _, mem := range uniqueMemories {
					userMemoryAnswer += fmt.Sprintf("• %s\n", mem.Content)
				}
				return userMemoryAnswer, nil
			}
		} else {
			log.Printf("No user memories found")
		}
	}

	// Handle correction request - search both knowledge base and distilled memories
	if isCorrection {
		log.Printf("🔧 Detected correction request")

		// Search for relevant content in knowledge base
		kbResults, err := kb.Search(ctx, tenantID, question)
		if err != nil {
			log.Printf("Knowledge base search failed: %v", err)
		}
		log.Printf("📝 Found %d knowledge base chunks for correction", len(kbResults))

		// Search for relevant distilled memories
		var memoryResults []*repositories.DistilledMemory
		if kb.distilledRepo != nil {
			embedding, embedErr := kb.embedding.EmbedWithPrefix(ctx, question, "query:")
			if embedErr == nil {
				memories, searchErr := kb.distilledRepo.SearchByVector(ctx, embedding, tenantID, 10)
				if searchErr == nil && len(memories) > 0 {
					memoryResults = memories
					log.Printf("👤 Found %d user distilled memories for correction", len(memories))
				} else {
					log.Printf("No user memories found or error: %v", searchErr)
				}
			}
		}

		if len(kbResults) == 0 && len(memoryResults) == 0 {
			return "No relevant information found for correction. Please provide more details about what needs to be corrected.", nil
		}

		// Build correction context
		var correctionContext strings.Builder
		correctionContext.WriteString("CORRECTION REQUEST: The user wants to correct or update information.\n\n")

		if len(kbResults) > 0 {
			correctionContext.WriteString("Current Knowledge Base Information:\n")
			for idx, result := range kbResults {
				idStr := result.ID
				if idStr == "" {
					idStr = fmt.Sprintf("KB:%d", idx+1)
				} else {
					idStr = fmt.Sprintf("KB:%s", idStr)
				}
				fmt.Fprintf(&correctionContext, "  [%s] %s (Score: %.3f)\n", idStr, result.Content, result.Score)
			}
			correctionContext.WriteString("\n")
		}
		if len(memoryResults) > 0 {
			correctionContext.WriteString("Current User Memory (to potentially update):\n")
			for _, mem := range memoryResults {
				label := fmt.Sprintf("MEM:%s", mem.ID)
				fmt.Fprintf(&correctionContext, "  [%s] %s (Type: %s, Importance: %.2f)\n",
					label, mem.Content, mem.MemoryType, mem.Importance)
			}
			correctionContext.WriteString("\n")
		}

		fmt.Fprintf(&correctionContext, `User correction: %s

Based on the user's correction, output ONLY the needed changes.
CRITICAL RULES:
- Output each command AT MOST ONCE (no duplicates).
- Each target appears in only ONE UPDATE or DELETE command.
- Do NOT repeat the same command.

Formats:
UPDATE: MEM:<id> -> <corrected content>
DELETE: MEM:<id>
UPDATE: KB:<id> -> <corrected content>
DELETE: KB:<id>
CREATE: <new knowledge to store>

For UPDATE and CREATE commands, provide DETAILED and COMPLETE information.
Enrich with relevant context so it serves as a comprehensive replacement.

If no changes are needed, output only: NOCHANGE
`, question)

		llmResponse, llmErr := kb.llmClient.Generate(ctx, correctionContext.String())
		if llmErr != nil {
			log.Printf("LLM correction generation failed: %v, falling back to passive correction", llmErr)
			return "I detected you want to correct information, but I'm having trouble processing it right now. Please try again.", nil
		}

		log.Printf("📋 LLM correction response: %s", llmResponse)

		// Parse and execute correction commands
		updated, deleted, executed := kb.executeCorrectionCommands(ctx, tenantID, llmResponse, memoryResults)
		if !executed {
			return "Unable to process the correction. Please rephrase.", nil
		}

		var result strings.Builder
		result.WriteString("✅ 知识库已更新：\n")
		if updated > 0 {
			fmt.Fprintf(&result, "  - 更新 %d 条记忆\n", updated)
		}
		if deleted > 0 {
			fmt.Fprintf(&result, "  - 删除 %d 条记忆\n", deleted)
		}
		if updated == 0 && deleted == 0 {
			result.WriteString("  (未执行修改，请检查输入)\n")
		}
		return result.String(), nil
	}
	// Step 1: Determine if RAG is needed using LLM
	needsRAG := true
	if kb.llmClient != nil {
		ragPrompt := fmt.Sprintf(`Question: %s

Needs knowledge base search? Answer YES/NO only:`, question)

		ragCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ragResponse, err := kb.llmClient.Generate(ragCtx, ragPrompt)
		cancel()

		if err == nil {
			needsRAG = strings.Contains(strings.ToUpper(ragResponse), "YES")
			log.Printf("RAG check: %s -> needs RAG: %v", question, needsRAG)
		}
	}

	var answer string
	if needsRAG {
		// Step 2: Retrieve relevant documents
		results, err := kb.Search(ctx, tenantID, question)
		if err != nil {
			return "", fmt.Errorf("retrieve documents: %w", err)
		}

		// If no documents found, inform user
		if len(results) == 0 {
			answer = "No relevant information found in the knowledge base. Please try rephrasing your question."
		} else {
			// Step 3: Build context from retrieved documents
			var contextBuilder strings.Builder
			for i, result := range results {
				fmt.Fprintf(&contextBuilder, "[Document %d - Score: %.3f]\n%s\n\n", i+1, result.Score, result.Content)
			}

			// Step 4: Build conversation history context
			var historyContext strings.Builder
			if kb.memMgr != nil && kb.sessionID != "" {
				messages, err := kb.memMgr.GetMessages(ctx, kb.sessionID)
				if err == nil && len(messages) > 0 {
					// Get last 5 messages for context
					start := len(messages) - 5
					if start < 0 {
						start = 0
					}
					for _, msg := range messages[start:] {
						fmt.Fprintf(&historyContext, "%s: %s\n", msg.Role, msg.Content)
					}
				}
			}

			// Step 5: Generate answer with LLM
			if kb.llmClient != nil {
				prompt := fmt.Sprintf(`You are a helpful assistant that answers questions based on the provided knowledge base, conversation history, and corrected user memories.

User Question: %s

Conversation History:
%s

Knowledge Base Context:
%s

CRITICAL INSTRUCTIONS:
1. Answer the user's question based on ALL provided context.
2. If any "User Memory (Type: knowledge)" items appear in the context, they represent USER-CORRECTED information that takes PRIORITY over the original knowledge base.
3. If the user's question contains INCORRECT ASSUMPTIONS or MISUNDERSTANDINGS, POLITELY CORRECT them with FACTS from the context.
4. DO NOT simply agree with the user if they are wrong - use facts to correct them.
5. DO NOT make up information that is not in the context.
6. If the context doesn't contain the answer, say "I don't have enough information to answer this question."
7. Be concise and direct.
8. Cite the relevant document numbers (e.g., [Document 1]) when using information from specific documents.

Answer:`, question, historyContext.String(), contextBuilder.String())

				// Call LLM with timeout
				genCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
				var genErr error
				answer, genErr = kb.llmClient.Generate(genCtx, prompt)
				cancel()

				if genErr != nil {
					log.Printf("LLM generation failed: %v, falling back to raw results", genErr)
					// Fall back to showing raw results if LLM fails
					answer = kb.formatRawResults(results)
				}
			} else {
				// Step 6: If LLM client is not available, format raw results
				answer = kb.formatRawResults(results)
			}
		}
	} else {
		// General conversation without RAG
		if kb.llmClient != nil {
			prompt := fmt.Sprintf(`You are a helpful assistant. Respond to the user's question naturally.

User: %s

Assistant:`, question)

			genCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			var genErr error
			answer, genErr = kb.llmClient.Generate(genCtx, prompt)
			cancel()

			if genErr != nil {
				answer = "I'm sorry, I'm having trouble generating a response right now. Please try again."
			}
		} else {
			answer = "LLM not configured. Please configure the llm section in config.yaml."
		}
	}

	// Step 7: Add assistant answer to memory
	if kb.memMgr != nil && kb.sessionID != "" {
		_ = kb.memMgr.AddMessage(ctx, kb.sessionID, "assistant", answer)

		// Step 8: Increment message count and check for distillation threshold
		kb.messageCount++
		// Trigger distillation at every threshold multiple (e.g., round 3, 6, 9, ...)
		if kb.config.Memory.EnableDistillation && kb.messageCount%kb.config.Memory.DistillationThreshold == 0 {
			log.Printf("🎯 [Memory Distillation] Conversation rounds reached threshold multiple (%d/%d), triggering memory distillation...",
				kb.messageCount, kb.config.Memory.DistillationThreshold)
			kb.triggerMemoryDistillation(ctx, tenantID)
			kb.distilledRounds = kb.messageCount // Update to current round
		}
	}

	return answer, nil
}

// formatRawResults formats search results for display when LLM is not available.
func (kb *KnowledgeBase) formatRawResults(results []*SearchResult) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Found %d relevant documents:\n\n", len(results))
	for i, result := range results {
		fmt.Fprintf(&output, "[Document %d - Score: %.3f]\n%s\n\n", i+1, result.Score, result.Content)
	}
	output.WriteString("Please configure LLM settings in config.yaml to enable natural language answers.")
	return output.String()
}

// triggerMemoryDistillation performs memory distillation when threshold is reached.
// It extracts key information from conversation history and stores it in the knowledge base.
func (kb *KnowledgeBase) triggerMemoryDistillation(ctx context.Context, tenantID string) {
	if kb.memMgr == nil || kb.sessionID == "" {
		slog.Warn("Memory not available for distillation")
		return
	}

	slog.Info("Starting memory distillation", "session_id", kb.sessionID)

	// Get conversation history
	messages, err := kb.memMgr.GetMessages(ctx, kb.sessionID)
	if err != nil || len(messages) == 0 {
		slog.Warn("No messages to distill", "error", err, "session_id", kb.sessionID)
		return
	}

	slog.Info("Found messages to distill", "count", len(messages), "session_id", kb.sessionID)

	// Use new distillation service API
	kb.distillMemory(ctx, tenantID, messages)
}

// distillMemory uses the new distillation service API to extract and store distilled memories.
func (kb *KnowledgeBase) distillMemory(ctx context.Context, tenantID string, messages []internalMemory.Message) {
	slog.Info("Using new distillation service API", "session_id", kb.sessionID)

	// Extract user ID from conversation history using unified logic
	var userID string
	for _, msg := range messages {
		if msg.Role == "user" {
			userID = kb.extractUserID(msg.Content)
			if userID != "" {
				break
			}
		}
	}

	if userID != "" {
		slog.Info("Extracted user ID from conversation", "user_id", userID)
	} else {
		slog.Info("No user ID extracted from conversation")
	}

	// Convert internal messages to API messages
	apiMessages := make([]memory.ConversationMessage, len(messages))
	for i, msg := range messages {
		apiMessages[i] = memory.ConversationMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	// Execute distillation with extracted user ID
	distilledMemories, err := kb.distillationSvc.DistillConversation(
		ctx,
		kb.sessionID,
		apiMessages,
		tenantID,
		userID,
	)

	if err != nil {
		slog.Error("New distillation failed", "error", err, "session_id", kb.sessionID)
		return
	}

	if len(distilledMemories) == 0 {
		slog.Info("No memories extracted from conversation", "session_id", kb.sessionID)
		return
	}

	slog.Info("New distillation completed", "memories_created", len(distilledMemories), "session_id", kb.sessionID)

	// Store each distilled memory
	for i, mem := range distilledMemories {
		slog.Info("Storing distilled memory",
			"index", i+1,
			"type", mem.Type,
			"importance", mem.Importance,
			"content_preview", truncateString(mem.Content, 100))

		// Generate embedding for the distilled memory
		var embedding []float64
		if kb.embedding != nil {
			embedding, err = kb.embedding.EmbedWithPrefix(ctx, mem.Content, "memory:")
			if err != nil {
				slog.Error("Failed to generate embedding for memory", "index", i+1, "error", err)
				embedding = make([]float64, 1024) // Fallback to zero vector
			} else {
				// Normalize embedding
				embedding = postgres.NormalizeVector(embedding)
				slog.Info("Generated embedding for memory", "index", i+1, "dimensions", len(embedding))
			}
		}

		// Convert DistilledMemory to DistilledMemory for storage
		distilledMem := &repositories.DistilledMemory{
			ID:               mem.ID,
			TenantID:         tenantID,
			UserID:           userID,
			SessionID:        kb.sessionID,
			Content:          mem.Content,
			Embedding:        embedding,
			EmbeddingModel:   kb.config.EmbeddingModel,
			EmbeddingVersion: 1,
			MemoryType:       string(mem.Type),
			Importance:       mem.Importance,
			Metadata: map[string]interface{}{
				"source":    "distillation_service",
				"memory_id": mem.ID,
			},
			AccessCount:    0,
			LastAccessedAt: nil,
			ExpiresAt:      time.Now().Add(90 * 24 * time.Hour), // 90 days expiration
			CreatedAt:      mem.CreatedAt,
		}

		if kb.distilledRepo != nil {
			if err := kb.distilledRepo.Create(ctx, distilledMem); err != nil {
				slog.Error("Failed to store distilled memory", "index", i+1, "error", err)
				continue
			}
			slog.Info("Successfully stored distilled memory", "memory_id", mem.ID, "user_id", userID)
		}
	}

	// Log metrics
	metrics := kb.distillationSvc.GetMetrics()
	slog.Info("Distillation metrics",
		"total_attempts", metrics.AttemptTotal,
		"total_success", metrics.SuccessTotal,
		"total_memories", metrics.MemoriesCreated)
}

// truncateString truncate string for log output
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// ListDocuments list all documents
func (kb *KnowledgeBase) ListDocuments(ctx context.Context, tenantID string) ([]*DocumentInfo, error) {
	query := `
		SELECT document_id, source, COUNT(*) as chunk_count
		FROM knowledge_chunks_1024
		WHERE tenant_id = $1
		GROUP BY document_id, source
		ORDER BY MAX(created_at) DESC
	`

	rows, err := kb.pool.Query(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows", "error", err)
		}
	}()

	var docs []*DocumentInfo
	for rows.Next() {
		var doc DocumentInfo
		if err := rows.Scan(&doc.ID, &doc.Source, &doc.ChunkCount); err != nil {
			continue
		}
		docs = append(docs, &doc)
	}

	return docs, nil
}

// DeleteDocument delete document
func (kb *KnowledgeBase) DeleteDocument(ctx context.Context, tenantID, documentID string) error {
	query := `DELETE FROM knowledge_chunks_1024 WHERE tenant_id = $1 AND document_id = $2`

	result, err := kb.pool.Exec(ctx, query, tenantID, documentID)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("document not found")
	}

	return nil
}

// StartChat start interactive Q&A with RAG and memory
func (kb *KnowledgeBase) StartChat(ctx context.Context, tenantID string) {
	scanner := NewTextScanner()

	// Check if LLM is configured
	llmEnabled := kb.llmClient != nil && kb.llmClient.IsEnabled()
	if llmEnabled {
		log.Println("LLM enabled - Using RAG (Retrieval + Generation) mode")
	} else {
		log.Println("LLM not configured - Using retrieval-only mode")
		log.Println("To enable LLM answers, configure llm section in config.yaml")
	}

	// Check if memory is enabled
	memoryEnabled := kb.memMgr != nil
	if memoryEnabled {
		log.Println("Memory enabled - Conversation history and distillation supported")
		// Start memory manager
		if err := kb.memMgr.Start(ctx); err != nil {
			log.Printf("Failed to start memory manager: %v", err)
		}
		// Create session
		sessionID, err := kb.memMgr.CreateSession(ctx, tenantID)
		if err != nil {
			log.Printf("Failed to create session: %v", err)
		} else {
			kb.sessionID = sessionID
			log.Printf("Session created: %s", sessionID)
		}
	}

	for {
		fmt.Print("\nYou: ")
		question, err := scanner.ReadLine()
		if err != nil {
			if err == io.EOF {
				log.Println("\nGoodbye!")
			} else {
				log.Printf("Error reading input: %v", err)
			}
			break
		}

		question = strings.TrimSpace(question)
		if question == "" {
			continue
		}

		if question == "exit" || question == "quit" {
			log.Println("Goodbye!")

			// Trigger final distillation before exit if enabled and there are undistilled conversations
			if kb.config.Memory.EnableDistillation {
				// Check if we need to distill: if rounds are not a multiple of threshold, there are undistilled conversations
				rounds := kb.messageCount
				threshold := kb.config.Memory.DistillationThreshold
				needsDistillation := rounds%threshold != 0

				if needsDistillation {
					log.Printf("🎯 [Memory Distillation] Exiting - triggering final distillation to preserve conversation data...")
					log.Printf("🎯 [Memory Distillation] Total rounds: %d, Threshold: %d, Last distilled round: %d, New rounds to distill: %d",
						rounds, threshold, kb.distilledRounds, rounds-(rounds/threshold)*threshold)
					kb.triggerMemoryDistillation(ctx, tenantID)
				} else {
					log.Printf("ℹ️ [Memory Distillation] Skipping exit distillation - conversation already distilled at round %d",
						rounds)
				}
			}

			break
		}

		// Set timeout for each RAG operation (120 seconds)
		ragCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		answer, err := kb.GenerateAnswer(ragCtx, tenantID, question)
		cancel()

		if err != nil {
			if err == context.DeadlineExceeded {
				log.Println("Response timeout. Please try again.")
			} else {
				log.Printf("Failed to generate answer: %v", err)
			}
			continue
		}

		// Display generated answer
		fmt.Printf("\nAssistant:\n%s\n", answer)
	}

	// Cleanup: stop memory manager
	if memoryEnabled && kb.memMgr != nil {
		if err := kb.memMgr.Stop(ctx); err != nil {
			log.Printf("Failed to stop memory manager: %v", err)
		}
	}
}

// Close close connection
func (kb *KnowledgeBase) Close() error {
	// Stop memory manager if running
	if kb.memMgr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = kb.memMgr.Stop(ctx)
	}
	// Close database connection
	return kb.pool.Close()
}

// chunkDocument chunk document
func (kb *KnowledgeBase) chunkDocument(content string, chunkSize, chunkOverlap int) []*Chunk {
	var chunks []*Chunk
	runes := []rune(content)
	contentLength := len(runes)

	for i := 0; i < contentLength; i += (chunkSize - chunkOverlap) {
		end := i + chunkSize
		if end > contentLength {
			end = contentLength
		}

		chunkText := string(runes[i:end])
		if strings.TrimSpace(chunkText) == "" {
			continue
		}

		chunks = append(chunks, &Chunk{
			Index: len(chunks),
			Text:  chunkText,
			Hash:  kb.generateHash(chunkText),
		})

		if end >= contentLength {
			break
		}
	}

	return chunks
}

// extractUserID extracts a consistent user ID from user text.
// It identifies self-introduction patterns and extracts only the user's name
// to ensure consistency across different conversation contexts.
//
// Args:
//
//	text - the user's message text.
//
// Returns:
//
//	string - the extracted user ID (lowercase name), or empty string if not found.
func (kb *KnowledgeBase) extractUserID(text string) string {
	if text == "" {
		return ""
	}

	lowerText := strings.ToLower(text)

	// Self-introduction patterns (order matters: more specific first)
	patterns := []struct {
		pattern      string
		stopKeywords []string
		nameOnly     bool // if true, extract only the first word (name)
	}{
		{
			pattern:      "my name is",
			stopKeywords: []string{",", ".", " and ", " i ", " i'm ", " i am ", " who ", " also ", " aka "},
			nameOnly:     true,
		},
		{
			pattern:      "i am",
			stopKeywords: []string{",", ".", " and ", " who ", " also ", " aka "},
			nameOnly:     true,
		},
		{
			pattern:      "i'm",
			stopKeywords: []string{",", ".", " and ", " who ", " also ", " aka "},
			nameOnly:     true,
		},
		{
			pattern:      "我叫",
			stopKeywords: []string{"，", "。", "，", "和", "也是", "又名"},
			nameOnly:     true,
		},
		{
			pattern:      "我是",
			stopKeywords: []string{"，", "。", "，", "和", "也是", "又名"},
			nameOnly:     true,
		},
	}

	for _, p := range patterns {
		if !strings.Contains(lowerText, p.pattern) {
			continue
		}

		parts := strings.Split(lowerText, p.pattern)
		if len(parts) <= 1 {
			continue
		}

		namePart := strings.TrimSpace(parts[1])
		if namePart == "" {
			continue
		}

		// Apply stop keywords to trim the name
		for _, keyword := range p.stopKeywords {
			if idx := strings.Index(namePart, keyword); idx > 0 {
				namePart = strings.TrimSpace(namePart[:idx])
				break // Stop at first keyword match
			}
		}

		// Extract only the first word if nameOnly is true
		if p.nameOnly {
			words := strings.Fields(namePart)
			if len(words) > 0 {
				return words[0] // Return only the first word (name)
			}
		}

		// Return the trimmed name part
		return namePart
	}

	return ""
}

// generateHash generate content hash (using SHA256)
func (kb *KnowledgeBase) generateHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// loadConfig load configuration file
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Set default values
	setDefaults(&config)

	return &config, nil
}

// setDefaults set configuration default values
func setDefaults(config *Config) {
	if config.Database.Host == "" {
		config.Database.Host = "localhost"
	}
	if config.Database.Port == 0 {
		config.Database.Port = 5433
	}
	if config.Database.User == "" {
		config.Database.User = "postgres"
	}
	if config.Database.Database == "" {
		config.Database.Database = "goagent"
	}
	if config.EmbeddingServiceURL == "" {
		config.EmbeddingServiceURL = "http://localhost:8000"
	}
	if config.EmbeddingModel == "" {
		config.EmbeddingModel = "e5-large-v2"
	}
	if config.LLM.Provider == "" {
		config.LLM.Provider = "openrouter"
	}
	if config.LLM.BaseURL == "" {
		config.LLM.BaseURL = "https://openrouter.ai/api/v1"
	}
	if config.LLM.Model == "" {
		config.LLM.Model = "meta-llama/llama-3.1-8b-instruct"
	}
	if config.LLM.Timeout == 0 {
		config.LLM.Timeout = 60
	}
	if config.LLM.MaxTokens == 0 {
		config.LLM.MaxTokens = 2048
	}
	if config.Memory.MaxHistory == 0 {
		config.Memory.MaxHistory = 10
	}
	if config.Memory.MaxSessions == 0 {
		config.Memory.MaxSessions = 100
	}
	if config.Memory.DistillationThreshold == 0 {
		config.Memory.DistillationThreshold = 5
	}
	if config.Knowledge.ChunkSize == 0 {
		config.Knowledge.ChunkSize = 1000
	}
	if config.Knowledge.ChunkOverlap == 0 {
		config.Knowledge.ChunkOverlap = 100
	}
	if config.Knowledge.TopK == 0 {
		config.Knowledge.TopK = 5
	}
	if config.Knowledge.MinScore == 0 {
		config.Knowledge.MinScore = 0.6
	}
}

// DocumentInfo document information
type DocumentInfo struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	ChunkCount int    `json:"chunk_count"`
}

// TextScanner simple text scanner
type TextScanner struct {
	scanner *bufio.Scanner
}

func NewTextScanner() *TextScanner {
	return &TextScanner{
		scanner: bufio.NewScanner(os.Stdin),
	}
}

func (s *TextScanner) ReadLine() (string, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return s.scanner.Text(), nil
}

// printUsage print usage instructions
func printUsage() {
	fmt.Println("Knowledge Base - Local Document Q&A System")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  go run main.go --save <doc_path>     Import a document")
	fmt.Println("  go run main.go --chat                Start interactive chat")
	fmt.Println("  go run main.go --list                List all documents")
	fmt.Println("  go run main.go --delete <doc_id>     Delete a document")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --tenant <id>    Tenant ID (default: default)")
	fmt.Println("  --config <path>  Config file path (default: config.yaml)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  go run main.go --save README.md")
	fmt.Println("  go run main.go --chat --tenant user123")
	fmt.Println("  go run main.go --list")
}

// NewExperienceRepositoryAdapter creates an adapter for ExperienceRepository interface
func NewExperienceRepositoryAdapter(repo *repositories.DistilledMemoryRepository) *experienceRepositoryAdapter {
	return &experienceRepositoryAdapter{
		repo: repo,
	}
}

// executeCorrectionCommands parses LLM correction commands and executes DB operations.
func (kb *KnowledgeBase) executeCorrectionCommands(ctx context.Context, tenantID, llmResponse string, memories []*repositories.DistilledMemory) (int, int, bool) {
	lines := strings.Split(llmResponse, "\n")
	var updated, deleted int
	hasCommand := false
	seen := make(map[string]bool) // dedup by command type + target

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "NOCHANGE" {
			continue
		}

		var dedupKey string

		switch {
		case strings.HasPrefix(line, "UPDATE:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "UPDATE:"))
			if idx := strings.Index(rest, "->"); idx > 0 {
				idPart := strings.TrimSpace(rest[:idx])
				newContent := strings.TrimSpace(rest[idx+2:])
				if newContent == "" {
					continue
				}
				dedupKey = "UPDATE:" + idPart
				if seen[dedupKey] {
					continue
				}
				seen[dedupKey] = true
				hasCommand = true

				switch {
				case strings.HasPrefix(idPart, "MEM:"):
					memID := strings.TrimSpace(strings.TrimPrefix(idPart, "MEM:"))
					if !isValidUUID(memID) {
						log.Printf("⚠️  Skipping UPDATE MEM: invalid UUID: %s", memID)
						continue
					}
					if err := kb.updateMemory(ctx, tenantID, memID, newContent); err != nil {
						log.Printf("❌ Failed to update memory %s: %v", memID, err)
					} else {
						updated++
					}
				case strings.HasPrefix(idPart, "KB:"):
					chunkID := strings.TrimSpace(strings.TrimPrefix(idPart, "KB:"))
					if !isValidUUID(chunkID) {
						log.Printf("⚠️  Skipping UPDATE KB: invalid UUID: %s", chunkID)
						continue
					}
					if err := kb.updateKnowledgeChunk(ctx, chunkID, newContent); err != nil {
						log.Printf("❌ Failed to update KB chunk %s: %v", chunkID, err)
					} else {
						updated++
					}
				}
			}

		case strings.HasPrefix(line, "DELETE:"):
			idPart := strings.TrimSpace(strings.TrimPrefix(line, "DELETE:"))
			dedupKey = "DELETE:" + idPart
			if seen[dedupKey] {
				continue
			}
			seen[dedupKey] = true
			hasCommand = true

			switch {
			case strings.HasPrefix(idPart, "MEM:"):
				memID := strings.TrimSpace(strings.TrimPrefix(idPart, "MEM:"))
				if !isValidUUID(memID) {
					log.Printf("⚠️  Skipping DELETE MEM: invalid UUID: %s", memID)
					continue
				}
				if err := kb.distilledRepo.Delete(ctx, tenantID, memID); err != nil {
					log.Printf("❌ Failed to delete memory %s: %v", memID, err)
				} else {
					deleted++
				}
			case strings.HasPrefix(idPart, "KB:"):
				chunkID := strings.TrimSpace(strings.TrimPrefix(idPart, "KB:"))
				if !isValidUUID(chunkID) {
					log.Printf("⚠️  Skipping DELETE KB: invalid UUID: %s", chunkID)
					continue
				}
				if err := kb.repo.Delete(ctx, chunkID); err != nil {
					log.Printf("❌ Failed to delete KB chunk %s: %v", chunkID, err)
				} else {
					deleted++
				}
			}

		case strings.HasPrefix(line, "CREATE:"):
			newContent := strings.TrimSpace(strings.TrimPrefix(line, "CREATE:"))
			if newContent == "" {
				continue
			}
			dedupKey = "CREATE:" + newContent
			if seen[dedupKey] {
				continue
			}
			seen[dedupKey] = true
			hasCommand = true

			if err := kb.createCorrectionMemory(ctx, tenantID, newContent); err != nil {
				log.Printf("❌ Failed to create correction memory: %v", err)
			} else {
				updated++
			}
		}
	}

	return updated, deleted, hasCommand
}

// isValidUUID checks if a string is a valid UUID.
func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// updateMemory updates a distilled memory with corrected content.
func (kb *KnowledgeBase) updateMemory(ctx context.Context, tenantID, memID, newContent string) error {
	all, err := kb.distilledRepo.GetByUserID(ctx, tenantID, "", 100)
	if err != nil {
		return fmt.Errorf("lookup memories: %w", err)
	}
	var target *repositories.DistilledMemory
	for _, m := range all {
		if m.ID == memID {
			target = m
			break
		}
	}
	if target == nil {
		return fmt.Errorf("memory %s not found", memID)
	}

	embedding, err := kb.embedding.EmbedWithPrefix(ctx, newContent, "passage:")
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}

	target.Content = newContent
	target.Embedding = embedding
	target.Importance = 0.8
	if target.Metadata == nil {
		target.Metadata = make(map[string]interface{})
	}
	target.Metadata["correction"] = true
	target.Metadata["corrected_at"] = time.Now().Format(time.RFC3339)

	return kb.distilledRepo.Update(ctx, target)
}

// createCorrectionMemory creates a new distilled memory with corrected information.
func (kb *KnowledgeBase) createCorrectionMemory(ctx context.Context, tenantID, newContent string) error {
	embedding, err := kb.embedding.EmbedWithPrefix(ctx, newContent, "passage:")
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}

	mem := &repositories.DistilledMemory{
		ID:               uuid.New().String(),
		TenantID:         tenantID,
		UserID:           "",
		SessionID:        "",
		Content:          newContent,
		Embedding:        embedding,
		EmbeddingModel:   "intfloat/e5-large",
		EmbeddingVersion: 1,
		MemoryType:       "knowledge",
		Importance:       0.8,
		Metadata: map[string]interface{}{
			"source":       "correction",
			"corrected_at": time.Now().Format(time.RFC3339),
		},
		AccessCount:    0,
		LastAccessedAt: nil,
		ExpiresAt:      time.Now().Add(90 * 24 * time.Hour),
		CreatedAt:      time.Now(),
	}

	return kb.distilledRepo.Create(ctx, mem)
}

// updateKnowledgeChunk updates a knowledge base chunk with corrected content.
func (kb *KnowledgeBase) updateKnowledgeChunk(ctx context.Context, chunkID, newContent string) error {
	// Fetch existing chunk first to preserve other fields
	existing, err := kb.repo.GetByID(ctx, chunkID)
	if err != nil {
		return fmt.Errorf("fetch chunk %s: %w", chunkID, err)
	}

	embedding, err := kb.embedding.EmbedWithPrefix(ctx, newContent, "passage:")
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}

	existing.Content = newContent
	existing.Embedding = embedding
	return kb.repo.Update(ctx, existing)
}

// experienceRepositoryAdapter adapts DistilledMemoryRepository to ExperienceRepository interface
type experienceRepositoryAdapter struct {
	repo *repositories.DistilledMemoryRepository
}

// SearchByVector implements ExperienceRepository interface
func (a *experienceRepositoryAdapter) SearchByVector(ctx context.Context, vector []float64, tenantID string, limit int) ([]*memory.Experience, error) {
	if vector == nil {
		return []*memory.Experience{}, nil
	}

	// Search for similar memories using the distilled repository
	memories, err := a.repo.SearchByVector(ctx, vector, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("search by vector: %w", err)
	}

	// Convert DistilledMemory to Experience
	experiences := make([]*memory.Experience, len(memories))
	for i, mem := range memories {
		experiences[i] = &memory.Experience{
			Problem:    extractProblemFromContent(mem.Content),
			Solution:   extractSolutionFromContent(mem.Content),
			Confidence: mem.Importance,
		}
	}

	return experiences, nil
}

// GetByMemoryType implements ExperienceRepository interface
func (a *experienceRepositoryAdapter) GetByMemoryType(ctx context.Context, tenantID string, memoryType memory.MemoryType) ([]*memory.Experience, error) {
	// Query distilled memories by memory type
	memories, err := a.repo.GetByMemoryType(ctx, tenantID, string(memoryType), 100)
	if err != nil {
		return nil, fmt.Errorf("get by memory type: %w", err)
	}

	// Convert DistilledMemory to Experience
	experiences := make([]*memory.Experience, len(memories))
	for i, mem := range memories {
		experiences[i] = &memory.Experience{
			Problem:    extractProblemFromContent(mem.Content),
			Solution:   extractSolutionFromContent(mem.Content),
			Confidence: mem.Importance,
		}
	}

	return experiences, nil
}

// CountByMemoryType implements ExperienceRepository interface
func (a *experienceRepositoryAdapter) CountByMemoryType(ctx context.Context, tenantID string, memoryType memory.MemoryType) (int, error) {
	return a.repo.CountByMemoryType(ctx, tenantID, string(memoryType))
}

// DeleteBatch implements ExperienceRepository interface
func (a *experienceRepositoryAdapter) DeleteBatch(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := a.repo.Delete(ctx, "default", id); err != nil {
			return err
		}
	}
	return nil
}

// Update implements ExperienceRepository interface
func (a *experienceRepositoryAdapter) Update(ctx context.Context, experience *memory.Experience) error {
	if experience == nil {
		return fmt.Errorf("experience cannot be nil")
	}

	// Search for existing memory by content pattern
	contentPattern := fmt.Sprintf("%s → %s", experience.Problem, experience.Solution)

	// Since DistilledMemoryRepository doesn't have a search by content method,
	// we need to get all memories and find the matching one
	// In a production system, this should be optimized with a proper database query

	// For now, we'll use GetByMemoryType to get all interaction memories
	memories, err := a.repo.GetByMemoryType(ctx, "default", "interaction", 1000)
	if err != nil {
		return fmt.Errorf("get memories for update: %w", err)
	}

	// Find the memory that matches the pattern
	var foundMemory *repositories.DistilledMemory
	for _, mem := range memories {
		if mem.Content == contentPattern {
			foundMemory = mem
			break
		}
	}

	if foundMemory == nil {
		return fmt.Errorf("experience not found")
	}

	// Update the memory with new values
	foundMemory.Importance = experience.Confidence

	// Add or update extraction method in metadata
	if foundMemory.Metadata == nil {
		foundMemory.Metadata = make(map[string]interface{})
	}
	foundMemory.Metadata["extraction_method"] = string(experience.ExtractionMethod)

	// Call the repository Update method
	return a.repo.Update(ctx, foundMemory)
}

// Delete implements ExperienceRepository interface
func (a *experienceRepositoryAdapter) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id cannot be empty")
	}

	// Call the repository Delete method
	return a.repo.Delete(ctx, "default", id)
}

// Create implements ExperienceRepository interface
func (a *experienceRepositoryAdapter) Create(ctx context.Context, experience *memory.Experience) error {
	// Convert Experience to DistilledMemory
	// Map solution type to interaction type to match database constraint
	distilledMem := &repositories.DistilledMemory{
		ID:         generateID(),
		TenantID:   "default",
		UserID:     "",
		SessionID:  "",
		Content:    fmt.Sprintf("%s → %s", experience.Problem, experience.Solution),
		MemoryType: "interaction",
		Importance: experience.Confidence,
		Metadata: map[string]interface{}{
			"extraction_method": string(experience.ExtractionMethod),
		},
	}

	return a.repo.Create(ctx, distilledMem)
}

// GetInternalRepository implements ExperienceRepository interface
func (a *experienceRepositoryAdapter) GetInternalRepository() interface{} {
	return a.repo
}

// extractProblemFromContent extracts problem from memory content
func extractProblemFromContent(content string) string {
	if content == "" {
		return ""
	}

	// Try to parse "problem → solution" format
	if strings.Contains(content, " → ") {
		parts := strings.SplitN(content, " → ", 2)
		return strings.TrimSpace(parts[0])
	}

	// Default: return first line or truncated content
	lines := strings.Split(content, "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return content
}

// extractSolutionFromContent extracts solution from memory content
func extractSolutionFromContent(content string) string {
	if content == "" {
		return ""
	}

	// Try to parse "problem → solution" format
	if strings.Contains(content, " → ") {
		parts := strings.SplitN(content, " → ", 2)
		return strings.TrimSpace(parts[1])
	}

	// Default: return second line or empty
	lines := strings.Split(content, "\n")
	if len(lines) > 1 {
		return strings.TrimSpace(lines[1])
	}
	return ""
}

// generateID generates a unique ID
func generateID() string {
	return uuid.New().String()
}
