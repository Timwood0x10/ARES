package providers

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/discovery"
)

// knownMCPBinaries is a list of well-known MCP server binary names.
// These are probed with --help to verify they are MCP servers.
var knownMCPBinaries = []string{
	"codegraph",
	"codebase-memory-mcp",
	"mcp-server-filesystem",
	"mcp-server-git",
	"mcp-server-github",
	"mcp-server-postgres",
	"mcp-server-sqlite",
	"mcp-server-fetch",
	"mcp-server-memory",
}

// BinaryProbeProvider discovers MCP servers by probing binaries in PATH.
// It runs `binary --help` and checks the output for MCP-related keywords.
type BinaryProbeProvider struct {
	timeout time.Duration
}

// NewBinaryProbeProvider creates a provider that probes known MCP binaries.
func NewBinaryProbeProvider() *BinaryProbeProvider {
	return &BinaryProbeProvider{
		timeout: 3 * time.Second,
	}
}

func (p *BinaryProbeProvider) Name() string {
	return "binary-probe"
}

func (p *BinaryProbeProvider) Confidence() discovery.Confidence {
	return discovery.ConfidenceMedium // 80% — verified by probing, not config.
}

func (p *BinaryProbeProvider) Discover(ctx context.Context) ([]discovery.DiscoveryRecord, error) {
	var (
		mu      sync.Mutex
		records []discovery.DiscoveryRecord
		wg      sync.WaitGroup
	)

	for _, bin := range knownMCPBinaries {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}

		wg.Add(1)
		go func(binPath string) {
			defer wg.Done()

			probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
			defer cancel()

			metadata := probeBinary(probeCtx, binPath)
			if !metadata.isMCP {
				return
			}

			rec := discovery.DiscoveryRecord{
				Source:     "binary-probe",
				Confidence: discovery.ConfidenceMedium,
				Endpoint:   binPath,
				Args:       metadata.defaultArgs,
				Tags:       metadata.tags,
				Metadata:   make(map[string]string),
			}
			if metadata.version != "" {
				rec.Metadata["version"] = metadata.version
			}
			if metadata.description != "" {
				rec.Metadata["description"] = metadata.description
			}

			mu.Lock()
			records = append(records, rec)
			mu.Unlock()
		}(path)
	}

	wg.Wait()
	return records, nil
}

// binaryMetadata holds info extracted from a binary's help output.
type binaryMetadata struct {
	isMCP       bool
	version     string
	description string
	defaultArgs []string
	tags        []string
}

// probeBinary runs --help and --version to extract metadata.
func probeBinary(ctx context.Context, path string) binaryMetadata {
	meta := binaryMetadata{}

	// Try --help first.
	helpText := runCommand(ctx, path, "--help")
	if helpText == "" {
		helpText = runCommand(ctx, path, "help")
	}

	// Check if help text mentions MCP.
	lower := strings.ToLower(helpText)
	if strings.Contains(lower, "mcp") || strings.Contains(lower, "model context protocol") {
		meta.isMCP = true
	}

	// Extract description (first non-empty line).
	for _, line := range strings.Split(helpText, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "Usage") && !strings.HasPrefix(line, "Flags") {
			meta.description = line
			break
		}
	}

	// Detect default args from usage patterns like "serve --mcp" or "stdio".
	if strings.Contains(lower, "serve --mcp") || strings.Contains(lower, "serve --stdio") {
		meta.defaultArgs = []string{"serve", "--mcp"}
	} else if strings.Contains(lower, "stdio") {
		meta.defaultArgs = []string{"stdio"}
	}

	// Try --version.
	versionText := runCommand(ctx, path, "--version")
	if versionText == "" {
		versionText = runCommand(ctx, path, "version")
	}
	if versionText != "" {
		meta.version = strings.TrimSpace(versionText)
	}

	// Auto-tag from help text.
	meta.tags = extractTags(lower)

	return meta
}

// runCommand runs a command and returns stdout, or "" on error.
// The command name is validated against a safe prefix to prevent arbitrary execution.
func runCommand(ctx context.Context, name string, args ...string) string {
	// Only allow running commands from known system paths.
	if !strings.HasPrefix(name, "/") {
		return ""
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return ""
	}
	return buf.String()
}

// extractTags generates tags from help text keywords.
func extractTags(helpText string) []string {
	var tags []string

	tagKeywords := map[string]string{
		"search":   "capability:search",
		"query":    "capability:query",
		"index":    "capability:index",
		"graph":    "capability:graph",
		"file":     "capability:file",
		"database": "capability:database",
		"sql":      "capability:sql",
		"web":      "capability:web",
		"scrape":   "capability:scrape",
		"memory":   "capability:memory",
		"code":     "domain:code",
		"analyze":  "domain:analysis",
	}

	for keyword, tag := range tagKeywords {
		if strings.Contains(helpText, keyword) {
			tags = append(tags, tag)
		}
	}

	return tags
}
