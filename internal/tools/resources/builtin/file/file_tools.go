package builtin

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"goagentx/internal/tools/resources/base"
	"goagentx/internal/tools/resources/core"
)

// FileTools provides file system operations.
type FileTools struct {
	*base.BaseTool
	allowedDir string
}

// FileToolsOption is a functional option for FileTools.
type FileToolsOption func(*FileTools)

// WithAllowedDir sets the allowed directory for security checks.
func WithAllowedDir(dir string) FileToolsOption {
	return func(ft *FileTools) {
		ft.allowedDir = dir
	}
}

// NewFileTools creates a new FileTools tool.
func NewFileTools(opts ...FileToolsOption) *FileTools {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (read, write, list)",
				Enum:        []interface{}{"read", "write", "list"},
			},
			"file_path": {
				Type:        "string",
				Description: "Absolute path to the file",
			},
			"directory_path": {
				Type:        "string",
				Description: "Absolute path to the directory (for list operation)",
			},
			"content": {
				Type:        "string",
				Description: "Content to write (for write operation)",
			},
			"mode": {
				Type:        "string",
				Description: "Write mode: 'write' (overwrite) or 'append'",
				Default:     "write",
				Enum:        []interface{}{"write", "append"},
			},
			"offset": {
				Type:        "integer",
				Description: "Starting line number for read (0-based)",
			},
			"limit": {
				Type:        "integer",
				Description: "Maximum number of lines to read",
			},
			"recursive": {
				Type:        "boolean",
				Description: "List directories recursively",
				Default:     false,
			},
			"include_hidden": {
				Type:        "boolean",
				Description: "Include hidden files (starting with .)",
				Default:     false,
			},
			"pattern": {
				Type:        "string",
				Description: "Glob pattern to filter files (e.g., '*.go', 'test_*')",
			},
		},
		Required: []string{"operation"},
	}

	ft := &FileTools{
		BaseTool: base.NewBaseToolWithCapabilities("file_tools", "Read, write, and list files and directories", core.CategorySystem, []core.Capability{core.CapabilityFile}, params),
	}

	for _, opt := range opts {
		opt(ft)
	}

	return ft
}

// Execute performs the file operation.
func (t *FileTools) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	switch operation {
	case "read":
		return t.readFile(ctx, params)
	case "write":
		return t.writeFile(ctx, params)
	case "list":
		return t.listFiles(ctx, params)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// readFile reads content from a file.
func (t *FileTools) readFile(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	filePath, ok := params["file_path"].(string)
	if !ok || filePath == "" {
		return core.NewErrorResult("file_path is required for read operation"), nil
	}

	// Convert relative path to absolute path
	if !filepath.IsAbs(filePath) {
		absPath, err := filepath.Abs(filePath)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve absolute path: %v", err)), nil
		}
		filePath = absPath
	}

	filePath = filepath.Clean(filePath)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// Try to find similar files in the same directory
		dir := filepath.Dir(filePath)
		baseName := filepath.Base(filePath)
		suggestions := findSimilarFiles(dir, baseName)

		if len(suggestions) > 0 {
			return core.NewErrorResult(fmt.Sprintf("file not found: %s\n\nDid you mean:\n  - %s", filePath, strings.Join(suggestions, "\n  - "))), nil
		}

		return core.NewErrorResult(fmt.Sprintf("file not found: %s", filePath)), nil
	}

	// Security: validate path is within allowed directory
	if t.allowedDir != "" {
		absPath, err := filepath.Abs(filePath)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve absolute path: %v", err)), nil
		}
		absDir, err := filepath.Abs(t.allowedDir)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve allowed directory: %v", err)), nil
		}
		if !strings.HasPrefix(absPath, absDir) {
			return core.NewErrorResult(fmt.Sprintf("access denied: path %s is outside allowed directory %s", filePath, t.allowedDir)), nil
		}
	}

	// Read file
	content, err := os.ReadFile(filePath) // #nosec G304
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	// Process offset and limit if provided
	lines := strings.Split(string(content), "\n")
	offset := getInt(params, "offset", 0)
	limit := getInt(params, "limit", len(lines))

	// Validate offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(lines) {
		return core.NewErrorResult("offset exceeds file length"), nil
	}

	// Validate limit
	if limit <= 0 {
		limit = len(lines) - offset
	}

	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}

	// Return requested lines
	resultLines := lines[offset:end]
	totalLines := len(lines)

	return core.NewResult(true, map[string]interface{}{
		"operation":   "read",
		"file_path":   filePath,
		"content":     strings.Join(resultLines, "\n"),
		"lines":       resultLines,
		"line_count":  len(resultLines),
		"total_lines": totalLines,
		"offset":      offset,
		"limit":       limit,
	}), nil
}

// writeFile writes content to a file.
func (t *FileTools) writeFile(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	filePath, ok := params["file_path"].(string)
	if !ok || filePath == "" {
		return core.NewErrorResult("file_path is required for write operation"), nil
	}

	content, ok := params["content"].(string)
	if !ok {
		return core.NewErrorResult("content is required for write operation"), nil
	}

	// Convert relative path to absolute path
	if !filepath.IsAbs(filePath) {
		absPath, err := filepath.Abs(filePath)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve absolute path: %v", err)), nil
		}
		filePath = absPath
	}

	filePath = filepath.Clean(filePath)

	// Security: validate path is within allowed directory
	if t.allowedDir != "" {
		absPath, err := filepath.Abs(filePath)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve absolute path: %v", err)), nil
		}
		absDir, err := filepath.Abs(t.allowedDir)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve allowed directory: %v", err)), nil
		}
		if !strings.HasPrefix(absPath, absDir) {
			return core.NewErrorResult(fmt.Sprintf("access denied: path %s is outside allowed directory %s", filePath, t.allowedDir)), nil
		}
	}

	// Get write mode
	mode := getString(params, "mode")
	if mode == "" {
		mode = "write"
	}

	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to create directory: %v", err)), nil
	}

	var writeErr error
	if mode == "append" {
		// Append mode
		file, openErr := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) // #nosec G304
		if openErr != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to open file: %v", openErr)), nil
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				slog.Error("failed to close file: ", "error", closeErr)
			}
		}()

		_, writeErr = file.WriteString(content)
		if writeErr != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to write to file: %v", writeErr)), nil
		}
	} else {
		// Write mode (overwrite)
		writeErr = os.WriteFile(filePath, []byte(content), 0600)
	}

	if writeErr != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to write file: %v", writeErr)), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":     "write",
		"file_path":     filePath,
		"mode":          mode,
		"bytes_written": len(content),
		"success":       true,
	}), nil
}

// listFiles lists files and directories in a directory.
func (t *FileTools) listFiles(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	dirPath, ok := params["directory_path"].(string)
	if !ok || dirPath == "" {
		return core.NewErrorResult("directory_path is required for list operation"), nil
	}

	// Convert relative path to absolute path
	if !filepath.IsAbs(dirPath) {
		absPath, err := filepath.Abs(dirPath)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve absolute path: %v", err)), nil
		}
		dirPath = absPath
	}

	dirPath = filepath.Clean(dirPath)

	// Security: validate path is within allowed directory
	if t.allowedDir != "" {
		absDir, err := filepath.Abs(dirPath)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve absolute path: %v", err)), nil
		}
		allowedAbs, err := filepath.Abs(t.allowedDir)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to resolve allowed directory: %v", err)), nil
		}
		if !strings.HasPrefix(absDir, allowedAbs) {
			return core.NewErrorResult(fmt.Sprintf("access denied: path %s is outside allowed directory %s", dirPath, t.allowedDir)), nil
		}
	}

	// Check if directory exists
	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return core.NewErrorResult(fmt.Sprintf("directory not found: %s", dirPath)), nil
		}
		return core.NewErrorResult(fmt.Sprintf("failed to access directory: %v", err)), nil
	}

	if !info.IsDir() {
		return core.NewErrorResult("path is not a directory"), nil
	}

	recursive := getBool(params, "recursive", false)
	includeHidden := getBool(params, "include_hidden", false)
	pattern := getString(params, "pattern")

	// Collect entries
	var files []map[string]interface{}
	var dirs []map[string]interface{}

	if recursive {
		// Walk directory recursively
		err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			relPath, err := filepath.Rel(dirPath, path)
			if err != nil {
				return err
			}

			// Skip root directory
			if relPath == "." {
				return nil
			}

			// Check hidden files
			if !includeHidden {
				base := filepath.Base(path)
				if strings.HasPrefix(base, ".") {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}

			// Check pattern
			if pattern != "" {
				matched, err := filepath.Match(pattern, filepath.Base(path))
				if err != nil {
					return err
				}
				if !matched {
					if info.IsDir() {
						return nil
					}
					return nil
				}
			}

			entry := map[string]interface{}{
				"path":     path,
				"rel_path": relPath,
				"name":     info.Name(),
				"size":     info.Size(),
				"mode":     info.Mode().String(),
				"modified": info.ModTime(),
				"is_dir":   info.IsDir(),
			}

			if info.IsDir() {
				dirs = append(dirs, entry)
			} else {
				files = append(files, entry)
			}

			return nil
		})
	} else {
		// List only top-level
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to read directory: %v", err)), nil
		}

		for _, entry := range entries {
			name := entry.Name()

			// Check hidden files
			if !includeHidden && strings.HasPrefix(name, ".") {
				continue
			}

			// Check pattern
			if pattern != "" {
				matched, err := filepath.Match(pattern, name)
				if err != nil {
					return core.NewErrorResult(fmt.Sprintf("invalid pattern: %v", err)), nil
				}
				if !matched {
					continue
				}
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			fullPath := filepath.Join(dirPath, name)
			entryMap := map[string]interface{}{
				"path":     fullPath,
				"name":     name,
				"size":     info.Size(),
				"mode":     info.Mode().String(),
				"modified": info.ModTime(),
				"is_dir":   entry.IsDir(),
			}

			if entry.IsDir() {
				dirs = append(dirs, entryMap)
			} else {
				files = append(files, entryMap)
			}
		}
	}

	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to list directory: %v", err)), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":   "list",
		"directory":   dirPath,
		"files":       files,
		"directories": dirs,
		"totals": map[string]interface{}{
			"directories": len(dirs),
			"files":       len(files),
		},
	}), nil
}

// getBool safely gets a boolean parameter.
func getBool(params map[string]interface{}, key string, defaultVal bool) bool {
	if val, ok := params[key].(bool); ok {
		return val
	}
	return defaultVal
}

// getString safely gets a string parameter.
func getString(params map[string]interface{}, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

// getInt safely gets an int parameter.
func getInt(params map[string]interface{}, key string, defaultVal int) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

// findSimilarFiles finds files with similar names in the same directory.
func findSimilarFiles(dir, baseName string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	// Extract base name without extension
	nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))

	var suggestions []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		entryName := entry.Name()
		entryWithoutExt := strings.TrimSuffix(entryName, filepath.Ext(entryName))

		// Check if base name matches (case insensitive)
		if strings.EqualFold(nameWithoutExt, entryWithoutExt) {
			suggestions = append(suggestions, filepath.Join(dir, entryName))
		}
	}

	return suggestions
}
