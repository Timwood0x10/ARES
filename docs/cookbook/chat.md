# Chat Agent Cookbook

Build a conversational agent with memory in 20 lines.

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

	rt := sdk.MustNew(
		sdk.WithOpenAI("gpt-4o-mini"),
		sdk.WithDefaultMemory(),
	)
	defer rt.Close()

	agent := rt.NewAgent("chatbot",
		sdk.WithInstruction("You are a friendly assistant. Keep responses concise."),
	)

	result, err := agent.Run(ctx, "What is Go?")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}
```

## Key Points

- `WithDefaultMemory()` enables conversation history — the agent remembers context across calls.
- Switch provider: replace `WithOpenAI` with `WithOllama("llama3.2")` for local inference.
- `Result` includes token usage and latency for monitoring.
