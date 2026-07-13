// Command mdkb — a structure-aware markdown knowledge base with RAG.
//
// It recursively ingests markdown files into PostgreSQL + pgvector, parsing
// document structure (headings, code blocks, tables, lists) and chunking by
// section, then answers questions over the corpus via retrieval-augmented
// generation.
//
// Usage:
//
//	go run ./examples/12-markdown-knowledge-base --dir /path/to/notes
//	go run ./examples/12-markdown-knowledge-base --file /path/to/note.md
//	go run ./examples/12-markdown-knowledge-base --ask "how does the scheduler work?"
//	go run ./examples/12-markdown-knowledge-base --list
//
// Flags may be combined with --tenant and --config.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// cliOptions holds parsed command-line flags.
type cliOptions struct {
	configPath string
	tenantID   string
	dir        string
	file       string
	question   string
	list       bool
}

func main() {
	if err := run(); err != nil {
		slog.Error("mdkb failed", "error", err)
		os.Exit(1)
	}
}

// run parses flags, wires the knowledge base, and dispatches the selected mode.
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

// parseFlags reads command-line flags into a cliOptions value.
func parseFlags() cliOptions {
	var opts cliOptions
	flag.StringVar(&opts.configPath, "config",
		"examples/12-markdown-knowledge-base/config.yaml", "Path to the YAML config file")
	flag.StringVar(&opts.tenantID, "tenant", "default", "Tenant namespace for stored chunks")
	flag.StringVar(&opts.dir, "dir", "", "Directory of markdown files to ingest recursively")
	flag.StringVar(&opts.file, "file", "", "Single markdown file to ingest")
	flag.StringVar(&opts.question, "ask", "", "Ask a question against the knowledge base")
	flag.BoolVar(&opts.list, "list", false, "List stored documents for the tenant")
	flag.Parse()
	return opts
}

// dispatch routes to the selected operation based on the provided flags.
func dispatch(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	switch {
	case opts.dir != "":
		return runIngestDir(ctx, kb, opts)
	case opts.file != "":
		return runIngestFile(ctx, kb, opts)
	case strings.TrimSpace(opts.question) != "":
		return runAsk(ctx, kb, opts)
	case opts.list:
		return runList(ctx, kb, opts)
	default:
		printUsage()
		return nil
	}
}

// runIngestDir ingests a directory and prints a summary.
func runIngestDir(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	stats, err := kb.IngestDir(ctx, opts.tenantID, opts.dir)
	if err != nil {
		return err
	}
	fmt.Printf("Ingested %d files, %d chunks stored, %d skipped.\n",
		stats.Files, stats.Chunks, stats.Skipped)
	return nil
}

// runIngestFile ingests a single file and prints a summary.
func runIngestFile(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	stored, skipped, err := kb.IngestFile(ctx, opts.tenantID, opts.file)
	if err != nil {
		return err
	}
	fmt.Printf("Imported %s: %d chunks stored, %d skipped.\n", opts.file, stored, skipped)
	return nil
}

// runAsk answers a question and prints the answer with cited sources.
func runAsk(ctx context.Context, kb *KnowledgeBase, opts cliOptions) error {
	answer, err := kb.Ask(ctx, opts.tenantID, opts.question)
	if err != nil {
		return err
	}
	printAnswer(answer)
	return nil
}

// runList prints the stored documents for the tenant.
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

// printAnswer renders an answer and its sources to stdout.
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

// printUsage prints a short usage guide.
func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  --dir <path>    ingest a directory of markdown files recursively")
	fmt.Println("  --file <path>   ingest a single markdown file")
	fmt.Println("  --ask <text>    ask a question against the knowledge base")
	fmt.Println("  --list          list stored documents")
	fmt.Println("  --tenant <id>   tenant namespace (default \"default\")")
	fmt.Println("  --config <path> config file path")
}
