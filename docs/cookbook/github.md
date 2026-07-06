# GitHub Agent Cookbook

Interact with GitHub issues and PRs using tools.

## Code

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	rt := sdk.MustNew(sdk.WithOpenAI("gpt-4o-mini"))
	defer rt.Close()

	// Register a GitHub issue reader tool.
	rt.ToolRegistry().Register(tools.ToolFunc{
		ToolName: "get_issue",
		ToolDesc: "Get a GitHub issue by owner/repo/number",
		Fn: func(_ context.Context, p map[string]any) (any, error) {
			owner, _ := p["owner"].(string)
			repo, _ := p["repo"].(string)
			num, _ := p["number"].(float64)
			return fmt.Sprintf("Issue #%.0f in %s/%s: Sample issue title", num, owner, repo), nil
		},
	})

	agent := rt.NewAgent("github-bot",
		sdk.WithInstruction("Help users triage GitHub issues."),
	)
	result, err := agent.Run(ctx, "Get issue #42 from Timwood0x10/ares")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}
```

## Key Points

- Wrap any GitHub API call as a tool for the agent.
- Use `web_search` tool to look up documentation.
- Add more tools: `list_issues`, `create_comment`, `merge_pr`.
- For production, use the official `google/go-github` library in tool `Fn`.
