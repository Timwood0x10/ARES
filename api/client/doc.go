// Package client provides a library-style entry point for embedding GoAgent
// into other Go applications. It exposes modular service accessors via api/core
// interfaces, with implementations injected at construction time.
//
// Internal implementations are constructed by api/bootstrap; this package
// has zero direct imports from internal/.
//
// # Quick Start
//
// The simplest way to use the ARES client is to supply pre-built service
// implementations through the Config struct:
//
//	import (
//	    "github.com/Timwood0x10/ares/api/client"
//	    "github.com/Timwood0x10/ares/api/core"
//	)
//
//	cl, err := client.NewClient(&client.Config{
//	    LLM:    myLLM,      // implements core.LLMService
//	    Memory: myMemory,   // implements core.MemoryService
//	})
//	if err != nil { ... }
//	defer cl.Close(ctx)
//
//	llm, _ := cl.LLM()
//	resp, _ := llm.GenerateSimple(ctx, "Hello ARES")
//	fmt.Println(resp) // "Hello, ARES"
//
// # Service Accessors
//
// Each call returns the service interface directly — no HTTP, no serialization.
//
//	agent, _     := cl.Agent()         // → core.AgentService
//	memory, _    := cl.Memory()        // → core.MemoryService
//	retrieval, _ := cl.Retrieval()     // → core.RetrievalService
//	llm, _       := cl.LLM()           // → core.LLMService
//	workflow, _  := cl.Workflow()      // → core.WorkflowService
//	runtime, _   := cl.Runtime(cfg)    // → runtime service
//	healthy      := cl.Ping(ctx)       // → bool
//	report, _    := cl.Health(ctx)     // → *HealthReport
//
// # Agent Management
//
//	agentSvc, _ := cl.Agent()
//
//	// Create an agent.
//	agent, err := agentSvc.CreateAgent(ctx, &core.AgentConfig{
//	    ID:   "worker-1",
//	    Name: "Data Processor",
//	    Type: "sub",
//	})
//
//	// List agents.
//	agents, _, err := agentSvc.ListAgents(ctx, nil)
//
//	// Execute a task.
//	result, err := agentSvc.ExecuteTask(ctx, &core.Task{
//	    ID:      "task-1",
//	    AgentID: "worker-1",
//	    Type:    "process",
//	})
//
// # Session & Memory
//
//	memorySvc, _ := cl.Memory()
//
//	// Create a session.
//	sessionID, err := memorySvc.CreateSession(ctx, &core.SessionConfig{
//	    UserID: "user-123",
//	})
//
//	// Add messages.
//	memorySvc.AddMessage(ctx, sessionID, core.RoleUser, "What is ARES?")
//	memorySvc.AddMessage(ctx, sessionID, core.RoleAssistant, "An agent framework.")
//
//	// Retrieve conversation.
//	messages, err := memorySvc.GetMessages(ctx, sessionID, nil)
//
// # Knowledge Retrieval
//
//	retrievalSvc, _ := cl.Retrieval()
//
//	// Add knowledge.
//	item, err := retrievalSvc.AddKnowledge(ctx, &core.KnowledgeItem{
//	    TenantID: "t1",
//	    Content:  "ARES is a Go agent framework with genetic algorithms.",
//	})
//
//	// Search.
//	results, err := retrievalSvc.Search(ctx, "t1", "genetic algorithm")
//
// # LLM
//
//	llmSvc, _ := cl.LLM()
//
//	resp, err := llmSvc.Generate(ctx, &core.GenerateRequest{
//	    Model:    "llama3.2",
//	    Messages: []core.LLMMessage{{Role: "user", Content: "Hello"}},
//	})
//	fmt.Println(resp.Content)
//
// # Workflow Orchestration
//
//	workflowSvc, _ := cl.Workflow()
//
//	result, err := workflowSvc.Execute(ctx, &core.WorkflowRequest{
//	    WorkflowID: "data-pipeline",
//	    Input:      "input-data",
//	})
//
//	// List available workflows.
//	workflows, err := workflowSvc.ListWorkflows(ctx)
//
// # Runtime Lifecycle
//
//	rt, err := cl.Runtime(nil)
//	rt.Start(ctx)
//	rt.RegisterAgent(myAgent, myFactory)
//	defer rt.Stop()
//
// # Configuration File
//
// Load configuration from a YAML file:
//
//	cfg, err := client.LoadConfig("config.yaml")
//	cl, err := client.NewClient(&client.Config{
//	    BaseConfig: &cfg.BaseConfig,
//	    LLM:        buildLLM(cfg),
//	})
//
// The config file format is defined in ConfigFile.
package client
