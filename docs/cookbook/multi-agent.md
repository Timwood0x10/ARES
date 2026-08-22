# Multi-Agent Cookbook

Register agents by capability and dispatch tasks through the kernel scheduler.

## Code

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	rt := sdk.MustNew(sdk.WithOllama("llama3.2"))
	defer rt.Close()

	// Register agents by capability. Each capability maps to one agent.
	rt.RegisterAgent("researcher",
		sdk.WithInstruction("Find facts and data."),
	)
	rt.RegisterAgent("writer",
		sdk.WithInstruction("Write clear summaries."),
	)

	// Submit a task — the kernel routes it to the matching agent.
	result, err := rt.Submit(ctx, sdk.Task{
		Capability: "researcher",
		Input:      "Research Go 1.26 features",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}
```

## Key Points

- `RegisterAgent` binds an agent to a capability string.
- `Submit` dispatches a `Task` to the registered agent through the kernel scheduler.
- Any number of capabilities can be registered; each maps to exactly one agent.
- Agents can communicate peer-to-peer via the IPC bus (`agentipc`).
- Task recovery is automatic: agent death does not lose the task (checkpoint + lease).
