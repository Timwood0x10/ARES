// Command check tests the semantic analyzer against a natural-language query.
//
// Usage:
//
//	go run ./examples/tool-intelligence/cmd/check "从1累加到100万"
//
// Output format:
//
//	OK|query|goal|operation|capability1,capability2
//	FAIL|query|reason|error_message
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Timwood0x10/ares/internal/tools/planner"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("USAGE: check <query>")
		os.Exit(1)
	}
	query := strings.Join(os.Args[1:], " ")

	analyzer := planner.NewRuleBasedAnalyzer()
	intent, err := analyzer.Analyze(context.Background(), query)
	if err != nil {
		fmt.Printf("FAIL|%s|no_match|%v\n", query, err)
		os.Exit(1)
	}

	caps := strings.Join(intent.RequiredCapabilities, ",")
	fmt.Printf("OK|%s|%s|%s|%s\n", query, intent.Goal, intent.Operation, caps)
}
