# Coding Agent Cookbook

Generate and review code with a specialized coding agent.

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

	rt := sdk.MustNew(sdk.WithOpenAI("gpt-4o-mini"))
	defer rt.Close()

	agent := rt.NewAgent("coder",
		sdk.WithInstruction(`You are a senior Go developer.
Write clean, idiomatic code with proper error handling.
Include comments and tests where appropriate.`),
	)

	result, err := agent.Run(ctx,
		"Write a Go function that reads a JSON file and returns a struct")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}
```

## Key Points

- Specialized instructions produce better domain-specific results.
- Use `WithOpenAI("gpt-4o")` for complex coding tasks.
- Add tools like `file_tools` or `web_search` for context-aware coding.
- Combine with `WithDefaultMemory()` for multi-turn code reviews.
