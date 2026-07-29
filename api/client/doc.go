// Package client provides a library-style entry point for embedding ARES
// into other Go applications.
//
// All service fields in Config accept api/core interfaces, with zero direct
// imports from internal/. Build concrete implementations via api/bootstrap
// or your own adapters.
//
// Usage:
//
//	import (
//	    "github.com/Timwood0x10/ares/api/client"
//	    "github.com/Timwood0x10/ares/api/core"
//	)
//
//	// Option 1: supply pre-built services (recommended).
//	cl, err := client.NewClient(&client.Config{
//	    LLM:    myLLMService,    // implements core.LLMService
//	    Memory: myMemoryService, // implements core.MemoryService
//	})
//
//	// Option 2: load from config file, then build services externally.
//	cfg := &client.Config{BaseConfig: &core.BaseConfig{RequestTimeout: 30}}
//	cl, err = client.NewClient(cfg)
//
//	svc, err := cl.LLM()
//	resp, err := svc.Generate(ctx, &core.GenerateRequest{...})
//
// For runtime and workflow, use the Runtime() and Workflow() accessors:
//
//	rt, err := cl.Runtime(config, eventStore)
//	wf, err := cl.Workflow()
package client
