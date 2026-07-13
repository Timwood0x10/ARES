//nolint: errcheck // best-effort: Close/Start/Stop/AddMessage errors are not actionable
// Command knowledge-import — structure-aware markdown knowledge base
// with RAG, multi-agent team import, and dialog-based chat.
//
// Features:
//   - Smart markdown parsing: headings, code blocks, tables, lists, YAML frontmatter
//   - Section-first chunking: never splits code blocks or tables
//   - PostgreSQL + pgvector + embedding + RAG
//   - CLI: --save / --dir / --ask / --list / --chat / --team
//   - Chat mode: agent with tools (read_directory, read_note_file, import_knowledge, query_knowledge)
//   - Team mode: multi-agent leader + sub-agents for parallel import
//   - Default: --team --dir <path> when no mode specified
//
// Usage:
//   go run examples/11-knowledge-import/main.go --dir ./notes
//   go run examples/11-knowledge-import/main.go --save ./notes/arch.md
//   go run examples/11-knowledge-import/main.go --ask "how does it work?"
//   go run examples/11-knowledge-import/main.go --chat
//   go run examples/11-knowledge-import/main.go --team --dir ./notes
//   go run examples/11-knowledge-import/main.go --list
//
// --dir is the default mode when no other flag is provided.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/Timwood0x10/ares/api/tools"
	ares_memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/sdk"
)

// cliOptions holds parsed command-line flags.
type cliOptions struct {
	configPath string
	tenantID   string
	dir        string
	file       string
	question   string
	chat       bool
	team       bool
	list       bool
}

func main() {
	if err := run(); err != nil {
		slog.Error("knowledge-import failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	opts := parseFlags()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := LoadConfig(opts.configPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	kb, err := NewKnowledgeBase(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := kb.Close(); cerr != nil {
			slog.Warn("close knowledge base", "error", cerr)
		}
	}()

	return dispatch(ctx, kb, opts)
}

func parseFlags() cliOptions {
	var opts cliOptions
	flag.StringVar(&opts.configPath, "config",
		"examples/11-knowledge-import/config.yaml", "Path to the YAML config file")
	flag.StringVar(&opts.tenantID, "tenant", "default", "Tenant namespace")
	flag.StringVar(&opts.dir, "dir", "", "Directory of markdown files to ingest recursively")
	flag.StringVar(&opts.file, "file", "", "Single markdown file to ingest")
	flag.StringVar(&opts.question, "ask", "", "Ask a question against the knowledge base")
	flag.BoolVar(&opts.chat, "chat", false, "Start interactive chat with RAG + tools")
	flag.BoolVar(&opts.team, "team", false, "Multi-agent team import from directory")
	flag.BoolVar(&opts.list, "list", false, "List stored documents")
	flag.Parse()
	return opts
}

func dispatch(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	switch {
	case opts.chat:
		return runChat(ctx, kb, opts)
	case opts.team:
		return runTeam(ctx, kb, opts)
	case opts.file != "":
		return runIngestFile(ctx, kb, opts)
	case opts.dir != "":
		return runIngestDir(ctx, kb, opts)
	case strings.TrimSpace(opts.question) != "":
		return runAsk(ctx, kb, opts)
	case opts.list:
		return runList(ctx, kb, opts)
	default:
		printUsage()
		return nil
	}
}

func runIngestDir(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	stats, err := kb.IngestDir(ctx, opts.tenantID, opts.dir)
	if err != nil {
		return err
	}
	fmt.Printf("Ingested %d files, %d chunks stored, %d skipped.\n",
		stats.Files, stats.Chunks, stats.Skipped)
	return nil
}

func runIngestFile(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	stored, skipped, err := kb.IngestFile(ctx, opts.tenantID, opts.file)
	if err != nil {
		return err
	}
	fmt.Printf("Imported %s: %d chunks stored, %d skipped.\n", opts.file, stored, skipped)
	return nil
}

func runAsk(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	answer, err := kb.Ask(ctx, opts.tenantID, opts.question)
	if err != nil {
		return err
	}
	printAnswer(answer)
	return nil
}

func runList(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	docs, err := kb.ListDocuments(ctx, opts.tenantID)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		fmt.Println("(no documents stored)")
		return nil
	}
	fmt.Printf("Documents for tenant %q:\n", opts.tenantID)
	for _, d := range docs {
		fmt.Printf("  %-60s %d chunks\n", d.Source, d.Chunks)
	}
	return nil
}

func printAnswer(answer *Answer) {
	fmt.Printf("\nQ: %s\n\n%s\n", answer.Question, answer.Text)
	if len(answer.Sources) == 0 {
		return
	}
	fmt.Printf("\nSources (generated=%v):\n", answer.Generated)
	for _, s := range answer.Sources {
		fmt.Printf("  [%d] score %.3f  %s\n", s.Rank, s.Score, s.Path)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  --dir <path>    ingest a directory of markdown files recursively (default)")
	fmt.Println("  --file <path>   ingest a single markdown file")
	fmt.Println("  --ask <text>    ask a question against the knowledge base")
	fmt.Println("  --chat          start interactive chat with RAG + tools")
	fmt.Println("  --team --dir    multi-agent team import")
	fmt.Println("  --list          list stored documents")
	fmt.Println("  --tenant <id>   tenant namespace (default \"default\")")
	fmt.Println("  --config <path> config file path (default examples/11-knowledge-import/config.yaml)")
}

// ── Chat mode ─────────────────────────────────────────────────────────────

func runChat(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	rt, err := sdk.New(sdk.WithOllama("llama3.2"), sdk.WithTrace(true))
	if err != nil {
		return fmt.Errorf("SDK: %v", err)
	}
	defer rt.Close()

	registerTools(rt, kb, opts.tenantID)

	var memMgr ares_memory.MemoryManager
	var sessionID string
	if cfg := kb.cfg; cfg.Memory.Enabled {
		memMgr, _ = ares_memory.NewMemoryManager(&ares_memory.MemoryConfig{
			Enabled: true, Storage: "memory", MaxHistory: cfg.Memory.MaxHistory,
			MaxSessions: cfg.Memory.MaxSessions, SessionTTL: 24 * time.Hour,
			TaskTTL: 7 * 24 * time.Hour, VectorDim: 128,
		})
		if memMgr != nil {
			memMgr.Start(ctx)
			sessionID, _ = memMgr.CreateSession(ctx, opts.tenantID)
		}
	}

	slog.Info("Chat started. Ask questions or say: 把 xxx 目录导入知识库")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" || input == "exit" || input == "quit" {
			break
		}

		// Import request: use agent with tools.
		if strings.Contains(input, "导入") || strings.Contains(input, "import") || strings.Contains(input, "存") {
			agt := rt.NewAgent("assistant",
				sdk.WithInstruction("You are a knowledge base import assistant. To import files: first call read_directory to discover .md files, then for each file call read_note_file to get the content, then call import_knowledge with the file path and content."),
				sdk.WithTools(toolList()...),
			)
			result, err := agt.Run(ctx, input)
			if err != nil {
				slog.Warn("import agent error", "error", err)
				fmt.Printf("\nError: %v\n", err)
				continue
			}
			fmt.Printf("\nAssistant: %s\n", result.Output)
			continue
		}

		// Question: use RAG pipeline.
		answer, err := kb.Ask(ctx, opts.tenantID, input)
		if err != nil {
			slog.Warn("ask error", "error", err)
			fmt.Printf("\nError: %v\n", err)
			continue
		}
		printAnswer(answer)

		if memMgr != nil && sessionID != "" {
			memMgr.AddMessage(ctx, sessionID, "user", input)
			memMgr.AddMessage(ctx, sessionID, "assistant", answer.Text)
		}
	}
	if memMgr != nil {
		memMgr.Stop(ctx)
	}
	return nil
}

// ── Team mode ─────────────────────────────────────────────────────────────

func runTeam(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	dir := opts.dir
	if dir == "" {
		dir = "examples/11-knowledge-import/notes"
	}
	if !strings.HasPrefix(dir, "/") && !strings.HasPrefix(dir, ".") && !strings.Contains(dir, ":") {
		// Not a path, treat as relative.
	}

	rt, err := sdk.New(sdk.WithOllama("llama3.2"), sdk.WithTrace(true))
	if err != nil {
		return fmt.Errorf("SDK: %v", err)
	}
	defer rt.Close()

	registerTools(rt, kb, opts.tenantID)

	leader := rt.NewAgent("leader",
		sdk.WithInstruction("You are the team leader. Discover files and coordinate the import."),
		sdk.WithTools(toolList()...),
	)

	subs := make([]*sdk.Agent, 8)
	for i := range subs {
		subs[i] = rt.NewAgent(fmt.Sprintf("importer-%d", i+1),
			sdk.WithInstruction("You are an import specialist. Read files and import them."),
			sdk.WithTools(toolList()...),
		)
	}

	team := rt.NewTeam("import-team", leader, subs)
	team.WithTeamConfig(sdk.TeamConfig{
		Mode: sdk.ModeAutoSplit, VerifierIndex: 7, MaxConcurrency: 3,
	})

	task := fmt.Sprintf("Read all markdown files from %s and import them into tenant %s", dir, opts.tenantID)
	result, err := team.Run(ctx, task)
	if err != nil {
		return err
	}
	slog.Info("Team import done", "duration", result.Duration, "passed", result.Passed)
	return nil
}

// ── Tools ─────────────────────────────────────────────────────────────────

var (
	dirTool  tools.Tool
	fileTool tools.Tool
	impTool  tools.Tool
	qryTool  tools.Tool
)

func toolList() []tools.Tool {
	return []tools.Tool{dirTool, fileTool, impTool, qryTool}
}

func registerTools(rt *sdk.Runtime, kb *KnowledgeBase, tenantID string) {
	dirTool = tools.ToolFunc{
		ToolName: "read_directory",
		ToolDesc: "Recursively find all .md files under a directory.",
		ToolParams: map[string]any{
			"type": "object", "properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Directory path"},
			}, "required": []string{"path"},
		},
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			path, _ := params["path"].(string)
			if path == "" {
				return nil, fmt.Errorf("path required")
			}
			files, err := findMarkdownFiles(path)
			if err != nil {
				return nil, fmt.Errorf("walk: %w", err)
			}
			if len(files) == 0 {
				return "(no .md files)", nil
			}
			return strings.Join(files, "\n"), nil
		},
	}

	fileTool = tools.ToolFunc{
		ToolName: "read_note_file",
		ToolDesc: "Read a file's content.",
		ToolParams: map[string]any{
			"type": "object", "properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Full file path"},
			}, "required": []string{"path"},
		},
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			path, _ := params["path"].(string)
			if path == "" {
				return nil, fmt.Errorf("path required")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}
			return string(data), nil
		},
	}

	impTool = tools.ToolFunc{
		ToolName: "import_knowledge",
		ToolDesc: "Import markdown content into the knowledge base. Parses structure (headings, code blocks, tables), chunks by section, vectorizes, and stores in PostgreSQL.",
		ToolParams: map[string]any{
			"type": "object", "properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path or title"},
				"content": map[string]any{"type": "string", "description": "Markdown content"},
			}, "required": []string{"path", "content"},
		},
		Fn: func(ctx context.Context, params map[string]any) (any, error) {
			path, _ := params["path"].(string)
			content, _ := params["content"].(string)
			if path == "" || content == "" {
				return nil, fmt.Errorf("path and content required")
			}
			// Use 12's parser + chunker to parse and chunk the content.
			doc := ParseContent(path, content)
			chunks := ChunkDocument(doc, kb.cfg.Knowledge)
			docID := uuid.New().String()
			ok := 0
			for _, c := range chunks {
				if ok_, _ := kb.storeChunk(ctx, tenantID, docID, doc, c); ok_ {
					ok++
				}
			}
			return fmt.Sprintf("✅ Imported %d/%d sections from %s", ok, len(chunks), path), nil
		},
	}

	qryTool = tools.ToolFunc{
		ToolName: "query_knowledge",
		ToolDesc: "Search the knowledge base.",
		ToolParams: map[string]any{
			"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			}, "required": []string{"query"},
		},
		Fn: func(ctx context.Context, params map[string]any) (any, error) {
			query, _ := params["query"].(string)
			if query == "" {
				return nil, fmt.Errorf("query required")
			}
			answer, err := kb.Ask(ctx, tenantID, query)
			if err != nil {
				return nil, fmt.Errorf("search: %w", err)
			}
			if len(answer.Sources) == 0 {
				return "(no results)", nil
			}
			var b strings.Builder
			b.WriteString(answer.Text)
			b.WriteString("\n\nSources:\n")
			for _, s := range answer.Sources {
				fmt.Fprintf(&b, "  [%d] score %.3f  %s\n", s.Rank, s.Score, s.Path)
			}
			return b.String(), nil
		},
	}

	for _, t := range toolList() {
		rt.ToolRegistry().Register(t)
	}
}