package client_test

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/api/client"
	"github.com/Timwood0x10/ares/api/core"
)

// stubLLM implements core.LLMService for demonstration purposes.
type stubLLM struct{}

func (s *stubLLM) Generate(_ context.Context, _ *core.GenerateRequest) (*core.GenerateResponse, error) {
	return &core.GenerateResponse{Content: "Hello from ARES!"}, nil
}
func (s *stubLLM) GenerateSimple(_ context.Context, prompt string) (string, error) {
	return "Hello, " + prompt, nil
}
func (s *stubLLM) GenerateEmbedding(_ context.Context, _ *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	return &core.EmbeddingResponse{Embedding: []float32{0.1, 0.2, 0.3}}, nil
}
func (s *stubLLM) GetConfig() *core.LLMConfig    { return &core.LLMConfig{Provider: "ollama"} }
func (s *stubLLM) IsEnabled() bool               { return true }
func (s *stubLLM) GetProvider() core.LLMProvider { return "ollama" }
func (s *stubLLM) GetModel() string              { return "llama3.2" }

// stubAgent implements core.AgentService for demonstration purposes.
type stubAgent struct {
	agents map[string]*core.Agent
}

func newStubAgent() *stubAgent {
	return &stubAgent{agents: make(map[string]*core.Agent)}
}

func (s *stubAgent) CreateAgent(_ context.Context, cfg *core.AgentConfig) (*core.Agent, error) {
	a := &core.Agent{ID: cfg.ID, Name: cfg.Name, Type: cfg.Type, Status: core.AgentStatusReady}
	s.agents[cfg.ID] = a
	return a, nil
}
func (s *stubAgent) GetAgent(_ context.Context, id string) (*core.Agent, error) {
	a, ok := s.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", id)
	}
	return a, nil
}
func (s *stubAgent) UpdateAgent(_ context.Context, id string, _ map[string]interface{}) (*core.Agent, error) {
	a, ok := s.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent %s not found", id)
	}
	return a, nil
}
func (s *stubAgent) DeleteAgent(_ context.Context, id string) error {
	delete(s.agents, id)
	return nil
}
func (s *stubAgent) ListAgents(_ context.Context, _ *core.AgentFilter) ([]*core.Agent, *core.PaginationResponse, error) {
	list := make([]*core.Agent, 0, len(s.agents))
	for _, a := range s.agents {
		list = append(list, a)
	}
	return list, nil, nil
}
func (s *stubAgent) ExecuteTask(_ context.Context, _ *core.Task) (*core.TaskResult, error) {
	return &core.TaskResult{Success: true}, nil
}
func (s *stubAgent) GetTaskResult(_ context.Context, _ string) (*core.TaskResult, error) {
	return &core.TaskResult{Success: true}, nil
}

// stubMemory implements core.MemoryService for demonstration purposes.
type stubMemory struct {
	sessions map[string]*core.Session
	messages map[string][]*core.Message
}

func newStubMemory() *stubMemory {
	return &stubMemory{
		sessions: make(map[string]*core.Session),
		messages: make(map[string][]*core.Message),
	}
}

func (s *stubMemory) CreateSession(_ context.Context, cfg *core.SessionConfig) (string, error) {
	id := fmt.Sprintf("session-%s", cfg.UserID)
	s.sessions[id] = &core.Session{ID: id, UserID: cfg.UserID}
	return id, nil
}
func (s *stubMemory) GetSession(_ context.Context, id string) (*core.Session, error) {
	ses, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return ses, nil
}
func (s *stubMemory) UpdateSession(_ context.Context, _ *core.Session) error { return nil }
func (s *stubMemory) DeleteSession(_ context.Context, id string) error {
	delete(s.sessions, id)
	delete(s.messages, id)
	return nil
}
func (s *stubMemory) AddMessage(_ context.Context, sessionID string, role core.MessageRole, content string) error {
	s.messages[sessionID] = append(s.messages[sessionID], &core.Message{
		Role: role, Content: content, Time: time.Now(),
	})
	return nil
}
func (s *stubMemory) GetMessages(_ context.Context, sessionID string, _ *core.PaginationRequest) ([]*core.Message, error) {
	return s.messages[sessionID], nil
}
func (s *stubMemory) DistillTask(_ context.Context, _ string) (*core.DistilledTask, error) {
	return &core.DistilledTask{TaskID: "task-1"}, nil
}
func (s *stubMemory) SearchSimilarTasks(_ context.Context, _ *core.SearchQuery) ([]*core.SearchResult, error) {
	return []*core.SearchResult{{TaskID: "similar-task"}}, nil
}

// stubRetrieval implements core.RetrievalService for demonstration purposes.
type stubRetrieval struct {
	items []*core.KnowledgeItem
}

func newStubRetrieval() *stubRetrieval { return &stubRetrieval{} }

func (s *stubRetrieval) Search(_ context.Context, _, query string) ([]*core.RetrievalResult, error) {
	return []*core.RetrievalResult{{Content: "result for " + query, Score: 0.95}}, nil
}
func (s *stubRetrieval) SearchWithConfig(ctx context.Context, req *core.RetrievalRequest) ([]*core.RetrievalResult, error) {
	return s.Search(ctx, req.TenantID, req.Query)
}
func (s *stubRetrieval) AddKnowledge(_ context.Context, item *core.KnowledgeItem) (*core.KnowledgeItem, error) {
	item.ID = fmt.Sprintf("item-%d", len(s.items)+1)
	s.items = append(s.items, item)
	return item, nil
}
func (s *stubRetrieval) GetKnowledge(_ context.Context, _, itemID string) (*core.KnowledgeItem, error) {
	for _, item := range s.items {
		if item.ID == itemID {
			return item, nil
		}
	}
	return nil, fmt.Errorf("item %s not found", itemID)
}
func (s *stubRetrieval) UpdateKnowledge(_ context.Context, _ string, item *core.KnowledgeItem) (*core.KnowledgeItem, error) {
	return item, nil
}
func (s *stubRetrieval) DeleteKnowledge(_ context.Context, _, itemID string) error {
	for i, item := range s.items {
		if item.ID == itemID {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return nil
		}
	}
	return nil
}
func (s *stubRetrieval) ListKnowledge(_ context.Context, _ string, _ *core.KnowledgeFilter) ([]*core.KnowledgeItem, *core.PaginationResponse, error) {
	return s.items, nil, nil
}

// Example_basic demonstrates creating a client with a pre-built LLM service.
func Example_basic() {
	cl, err := client.NewClient(&client.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
		LLM:        &stubLLM{},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	svc, err := cl.LLM()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	resp, err := svc.GenerateSimple(context.Background(), "ARES")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println(resp)
	// Output: Hello, ARES
}

// Example_agent demonstrates agent lifecycle management.
func Example_agent() {
	cl, err := client.NewClient(&client.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
		Agent:      newStubAgent(),
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	svc, err := cl.Agent()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Create an agent.
	agent, err := svc.CreateAgent(context.Background(), &core.AgentConfig{
		ID: "worker-1", Name: "Worker", Type: "sub",
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Created agent: %s (status=%s)\n", agent.ID, agent.Status)

	// List agents.
	agents, _, err := svc.ListAgents(context.Background(), nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Total agents: %d\n", len(agents))

	// Output:
	// Created agent: worker-1 (status=ready)
	// Total agents: 1
}

// Example_session demonstrates session and message management.
func Example_session() {
	cl, err := client.NewClient(&client.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
		Memory:     newStubMemory(),
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	mem, err := cl.Memory()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Create a session.
	sessionID, err := mem.CreateSession(context.Background(), &core.SessionConfig{
		UserID: "alice",
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Session: %s\n", sessionID)

	// Add messages.
	_ = mem.AddMessage(context.Background(), sessionID, "user", "What is ARES?")
	_ = mem.AddMessage(context.Background(), sessionID, "assistant", "An agent framework.")

	// Get messages.
	msgs, err := mem.GetMessages(context.Background(), sessionID, nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Messages: %d\n", len(msgs))

	// Output:
	// Session: session-alice
	// Messages: 2
}

// Example_retrieval demonstrates knowledge base operations.
func Example_retrieval() {
	cl, err := client.NewClient(&client.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
		Retrieval:  newStubRetrieval(),
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	svc, err := cl.Retrieval()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Add a knowledge item.
	item, err := svc.AddKnowledge(context.Background(), &core.KnowledgeItem{
		TenantID: "t1",
		Content:  "ARES supports genetic algorithm evolution.",
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Added knowledge: %s\n", item.ID)

	// Search.
	results, err := svc.Search(context.Background(), "t1", "genetic algorithm")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Results: %d\n", len(results))
	fmt.Printf("Top result: %s\n", results[0].Content)

	// Output:
	// Added knowledge: item-1
	// Results: 1
	// Top result: result for genetic algorithm
}

// Example_health demonstrates health check and ping.
func Example_health() {
	cl, err := client.NewClient(&client.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer func() { _ = cl.Close(context.Background()) }()

	// Ping.
	ok := cl.Ping(context.Background())
	fmt.Printf("Ping: %v\n", ok)

	// Health report.
	report, err := cl.Health(context.Background())
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Health: %v\n", report.Healthy)

	// Output:
	// Ping: true
	// Health: true
}
