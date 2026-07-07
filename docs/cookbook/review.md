# Code Review Cookbook

Automate code review with a dedicated review agent.

## Code

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	rt := sdk.MustNew(sdk.WithOpenAI("gpt-4o-mini"))
	defer rt.Close()

	// Get git diff as review input.
	diff, _ := exec.Command("git", "diff", "HEAD~1").Output()

	agent := rt.NewAgent("reviewer",
		sdk.WithInstruction(`You are a senior code reviewer.
Focus on: correctness, security, performance, style.
Provide actionable feedback. Be concise.`),
	)

	result, err := agent.Run(ctx, fmt.Sprintf(
		"Review this diff:\n```\n%s\n```", string(diff)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}
```

## Key Points

- Pipe `git diff` output directly to the agent for PR reviews.
- Customize review focus areas in the instruction.
- Combine with `WithTools(webSearch)` to look up best practices.
- Can be integrated into CI pipelines for automated PR review.
