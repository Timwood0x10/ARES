# Memory Cookbook

Persist conversation context across multiple interactions.

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
		sdk.WithOllama("llama3.2"),
		sdk.WithDefaultMemory(),
	)
	defer rt.Close()

	agent := rt.NewAgent("assistant",
		sdk.WithInstruction("Remember what the user tells you."),
	)

	// First interaction — the agent remembers the name.
	r1, _ := agent.Run(ctx, "My name is Alice")
	fmt.Println("Q1:", r1.Output)

	// Second interaction — memory provides context.
	r2, _ := agent.Run(ctx, "What is my name?")
	fmt.Println("Q2:", r2.Output)
	// Expected: "Your name is Alice."
}
```

## Key Points

- `WithDefaultMemory()` stores session history in-memory.
- Memory includes user messages and assistant responses.
- `BuildContext` injects relevant conversation history into prompts.
- For production, switch to SQLite or PostgreSQL backend.
