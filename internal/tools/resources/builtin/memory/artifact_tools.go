package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Timwood0x10/ares/internal/runtime/memory"
	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// defaultArtifactReadRunes bounds how much of a retrieved artifact enters the
// prompt. Reading back the full original is exactly what compression avoids, so
// the reader is a WINDOW onto the artifact, not a bypass around the bound: the
// model asks for the part it needs, and a truncated answer says so.
const defaultArtifactReadRunes = 4000

// ArtifactRead resolves an artifact ref (the `ref:` a compressed observation
// carries) to its verbatim bytes.
//
// This tool is what makes the chat path's contract REPLAYABLE instead of merely
// IDENTIFIABLE: without a reader, a digest proves the original exists and
// nothing can fetch it, which is a pointer-shaped lie. With it, the compressed
// marker is a real address, and the two paths (planner node ids, chat artifacts)
// answer to the same protocol.
//
// SECURITY BOUNDARY: a ref embeds its session id, and a model-supplied ref is
// untrusted input. Ownership is verified against the session's recorded user
// before any bytes are returned, and an unverifiable session fails closed —
// the same rules UserProfile documents for session_id.
type ArtifactRead struct {
	*base.BaseTool
	memoryMgr memory.MemoryManager
}

// NewArtifactRead creates the artifact_read tool.
func NewArtifactRead(memoryMgr memory.MemoryManager) *ArtifactRead {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"ref": {
				Type:        "string",
				Description: "Artifact ref to read, as shown in a compressed observation (artifact://<session>/<n>)",
			},
			"max_runes": {
				Type:        "integer",
				Description: "Maximum runes to return (default 4000); the reply states whether it was truncated",
				Default:     defaultArtifactReadRunes,
			},
		},
		Required: []string{"ref"},
	}

	t := &ArtifactRead{memoryMgr: memoryMgr}
	t.BaseTool = base.NewBaseToolWithCapabilities(
		"artifact_read",
		"Read the verbatim content behind an artifact ref from a compressed observation",
		core.CategoryMemory,
		[]core.Capability{core.CapabilityMemory},
		params,
	)
	return t
}

// Execute resolves the ref and returns a bounded window of the content.
func (t *ArtifactRead) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	ref, _ := params["ref"].(string)
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return core.NewErrorResult("ref is required: pass the artifact://... value from a compressed observation"), nil
	}

	sessionID, ok := memctx.ArtifactSessionID(ref)
	if !ok {
		return core.NewErrorResult(fmt.Sprintf("not an artifact ref: %q", ref)), nil
	}

	// Fail closed on BOTH sides, reading the owner before any bytes.
	//
	// The previous form was fail-OPEN in two ways that contradicted its own
	// comment: `caller != "" && owner != ""` meant an unattributed session
	// (owner "") was readable by anyone, and a caller with no user scope skipped
	// the check entirely. "Cannot determine the owner" is exactly the case that
	// must refuse, not the case that must be excused.
	//
	// The rule now matches UserProfile's (the sibling tool): resolve the caller,
	// error when there is none, then require a determinable owner equal to it.
	// An anonymous request therefore cannot read artifacts it cannot prove it
	// owns; the caller-side fallback to params["user_id"] is the same legacy
	// escape hatch UserProfile documents.
	caller := userctx.From(ctx)
	if caller == "" {
		caller, _ = params["user_id"].(string)
	}
	if caller == "" {
		return core.NewErrorResult("user_id is required: the request carries no user and none was supplied"), nil
	}

	owner, err := t.memoryMgr.SessionOwner(ctx, sessionID)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("artifact ownership unavailable: %v", err)), nil
	}
	if owner == "" {
		return core.NewErrorResult("artifact ref has no attributable owner; refusing to read"), nil
	}
	if owner != caller {
		return core.NewErrorResult("artifact ref belongs to another user's session"), nil
	}

	content, err := t.memoryMgr.GetArtifact(ctx, ref)
	switch {
	case errors.Is(err, memctx.ErrArtifactNotFound):
		return core.NewErrorResult("artifact no longer retained (session expired or the byte cap evicted it); rely on the compressed summary"), nil
	case errors.Is(err, memctx.ErrInvalidArtifactRef):
		return core.NewErrorResult(fmt.Sprintf("not an artifact ref: %q", ref)), nil
	case err != nil:
		return core.NewErrorResult(fmt.Sprintf("read artifact: %v", err)), nil
	}

	maxRunes := defaultArtifactReadRunes
	if v, ok := params["max_runes"].(float64); ok && int(v) > 0 {
		maxRunes = int(v)
	}

	total := utf8.RuneCountInString(content)
	body := content
	truncated := false
	if total > maxRunes {
		body = string([]rune(content)[:maxRunes])
		truncated = true
	}

	out := map[string]interface{}{
		"ref":         ref,
		"content":     body,
		"total_runes": total,
		"returned":    utf8.RuneCountInString(body),
		"truncated":   truncated,
	}
	if truncated {
		out["hint"] = fmt.Sprintf("truncated at %d of %d runes; raise max_runes for more", maxRunes, total)
	}
	return core.NewResult(true, out), nil
}
