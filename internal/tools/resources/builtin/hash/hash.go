// Package builtin - cryptographic hash and base64 encoding tools.
package builtin

import (
	"context"
	"crypto/md5"  //nolint:gosec // intentional hash tool supporting multiple algorithms
	"crypto/sha1" //nolint:gosec // intentional hash tool supporting multiple algorithms
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// HashTool provides cryptographic hash and base64 encoding operations.
type HashTool struct {
	*base.BaseTool
}

// NewHashTool creates a new HashTool.
func NewHashTool() *HashTool {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation: md5, sha1, sha256, sha512, base64_encode, base64_decode",
				Enum:        []interface{}{"md5", "sha1", "sha256", "sha512", "base64_encode", "base64_decode"},
			},
			"input": {
				Type:        "string",
				Description: "Input text to process",
			},
		},
		Required: []string{"operation", "input"},
	}

	return &HashTool{
		BaseTool: base.NewBaseToolWithCapabilities("hash_tool",
			"Compute cryptographic hashes (MD5, SHA1, SHA256, SHA512) and Base64 encode/decode",
			core.CategoryCore, []core.Capability{core.CapabilityText}, params),
	}
}

// Execute performs the hash or base64 operation.
func (t *HashTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	input, ok := params["input"].(string)
	if !ok {
		return core.NewErrorResult("input is required"), nil
	}

	switch operation {
	case "md5":
		return t.hashResult("md5", md5.New(), []byte(input)), nil //nolint:gosec // intentional hash tool
	case "sha1":
		return t.hashResult("sha1", sha1.New(), []byte(input)), nil //nolint:gosec // intentional hash tool
	case "sha256":
		return t.hashResult("sha256", sha256.New(), []byte(input)), nil
	case "sha512":
		return t.hashResult("sha512", sha512.New(), []byte(input)), nil
	case "base64_encode":
		return core.NewResult(true, map[string]interface{}{
			"operation": "base64_encode",
			"input":     input,
			"output":    base64.StdEncoding.EncodeToString([]byte(input)),
		}), nil
	case "base64_decode":
		decoded, err := base64.StdEncoding.DecodeString(input)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("base64 decode failed: %v", err)), nil
		}
		return core.NewResult(true, map[string]interface{}{
			"operation": "base64_decode",
			"input":     input,
			"output":    string(decoded),
		}), nil
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

func (t *HashTool) hashResult(algorithm string, h hash.Hash, data []byte) core.Result {
	h.Write(data)
	return core.NewResult(true, map[string]interface{}{
		"operation": algorithm,
		"input":     string(data),
		"output":    hex.EncodeToString(h.Sum(nil)),
	})
}

// IsIdempotent returns true since hashing has no side effects.
func (t *HashTool) IsIdempotent() bool { return true }
