# Multi-Agent Cookbook

Orchestrate a team with a leader and members.

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

	leader := rt.NewAgent("coordinator",
		sdk.WithInstruction("Plan tasks and synthesize results."),
	)
	researcher := rt.NewAgent("researcher",
		sdk.WithInstruction("Find facts and data."),
	)

	team := rt.NewTeam("team-alpha", leader, []*sdk.Agent{researcher})
	result, err := team.Run(ctx, "Research Go 1.26 features")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}
```

## Key Points

- `NewTeam` creates a leader-member group.
- Leader plans → members execute → leader synthesizes.
- Team supports any number of members.
- Each member has its own instruction for specialization.
