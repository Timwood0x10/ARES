// Package providers implements DiscoveryProvider for various sources.
package providers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Timwood0x10/ares/internal/discovery"
)

// FilesystemProvider scans local config files for MCP server definitions.
type FilesystemProvider struct {
	name        string
	confidence  discovery.Confidence
	configPaths []string
}

// NewClaudeProvider scans Claude Code config files.
func NewClaudeProvider(projectDir string) *FilesystemProvider {
	home, _ := os.UserHomeDir()
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".claude.json"))
	}
	if projectDir != "" {
		paths = append(paths, filepath.Join(projectDir, ".claude", "settings.json"))
	}
	return &FilesystemProvider{
		name:        "claude",
		confidence:  discovery.ConfidenceHigh,
		configPaths: paths,
	}
}

// NewCursorProvider scans Cursor IDE config files.
func NewCursorProvider() *FilesystemProvider {
	home, _ := os.UserHomeDir()
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".cursor", "mcp.json"))
	}
	return &FilesystemProvider{
		name:        "cursor",
		confidence:  discovery.ConfidenceHigh,
		configPaths: paths,
	}
}

// NewVSCodeProvider scans VS Code project config files.
func NewVSCodeProvider(projectDir string) *FilesystemProvider {
	paths := []string{}
	if projectDir != "" {
		paths = append(paths, filepath.Join(projectDir, ".vscode", "mcp.json"))
	}
	return &FilesystemProvider{
		name:        "vscode",
		confidence:  discovery.ConfidenceHigh,
		configPaths: paths,
	}
}

// NewARESProvider scans ARES's own registry file.
func NewARESProvider() *FilesystemProvider {
	home, _ := os.UserHomeDir()
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".ares", "mcp-registry.json"))
	}
	return &FilesystemProvider{
		name:        "ares",
		confidence:  discovery.ConfidenceMax,
		configPaths: paths,
	}
}

func (p *FilesystemProvider) Name() string {
	return p.name
}

func (p *FilesystemProvider) Confidence() discovery.Confidence {
	return p.confidence
}

func (p *FilesystemProvider) Discover(_ context.Context) ([]discovery.DiscoveryRecord, error) {
	var records []discovery.DiscoveryRecord
	for _, path := range p.configPaths {
		recs, err := scanConfigFile(path, p.name, p.confidence)
		if err != nil {
			continue
		}
		records = append(records, recs...)
	}
	return records, nil
}

// scanConfigFile reads a config file and extracts MCP server definitions.
func scanConfigFile(path, source string, conf discovery.Confidence) ([]discovery.DiscoveryRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Claude/Cursor format: {"mcpServers": {...}}
	var claudeCfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &claudeCfg); err == nil && len(claudeCfg.MCPServers) > 0 {
		records := make([]discovery.DiscoveryRecord, 0, len(claudeCfg.MCPServers))
		for _, sc := range claudeCfg.MCPServers {
			endpoint := sc.Command
			if endpoint == "" {
				endpoint = sc.URL
			}
			records = append(records, discovery.DiscoveryRecord{
				Source:     source,
				Confidence: conf,
				Endpoint:   endpoint,
				Args:       sc.Args,
			})
		}
		return records, nil
	}

	// ARES format: {"servers": [...]}
	var aresCfg struct {
		Servers []struct {
			Name    string   `json:"name"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(data, &aresCfg); err == nil && len(aresCfg.Servers) > 0 {
		records := make([]discovery.DiscoveryRecord, 0, len(aresCfg.Servers))
		for _, s := range aresCfg.Servers {
			endpoint := s.Command
			if endpoint == "" {
				endpoint = s.URL
			}
			records = append(records, discovery.DiscoveryRecord{
				Source:     source,
				Confidence: conf,
				Endpoint:   endpoint,
				Args:       s.Args,
			})
		}
		return records, nil
	}

	// VS Code format: {"servers": {"name": {...}}}
	var vscodeCfg struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(data, &vscodeCfg); err == nil && len(vscodeCfg.Servers) > 0 {
		records := make([]discovery.DiscoveryRecord, 0, len(vscodeCfg.Servers))
		for _, sc := range vscodeCfg.Servers {
			endpoint := sc.Command
			if endpoint == "" {
				endpoint = sc.URL
			}
			records = append(records, discovery.DiscoveryRecord{
				Source:     source,
				Confidence: conf,
				Endpoint:   endpoint,
				Args:       sc.Args,
			})
		}
		return records, nil
	}

	return nil, nil
}
