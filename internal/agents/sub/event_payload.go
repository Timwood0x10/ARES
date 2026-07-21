package sub

import (
	"encoding/json"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// maxEventTextFieldLen bounds the size of task/result text placed in events so
// payloads stay reasonable for the in-memory/PG event store.
const maxEventTextFieldLen = 4000

// distillTenantID returns the tenant scope used when storing distilled
// experiences produced from this agent's task events.
//
// Tenant strategy (v1, single-tenant): every experience is stored under
// ares_events.DefaultTenantID ("default"). This MUST match the tenant the GA's
// GuidanceProvider reads from, otherwise distilled hints are silently never
// consumed. The experience repository scopes every read by tenant_id, so both
// the write side (this emitter → distillation subscriber) and the read side
// (GuidanceProvider) must agree.
//
// Multi-tenant is intentionally out of scope here: it requires threading the
// caller's tenant through the GA's Mutate request context so the
// GuidanceProvider can resolve the correct tenant at hint-lookup time. Until
// then, forcing a single default tenant keeps the loop genuinely closed.
func distillTenantID() string {
	return ares_events.DefaultTenantID
}

// taskEventText extracts a best-effort textual description of a task for
// embedding and experience retrieval. Preference order:
//  1. an explicit request/query string in the task payload
//  2. the full payload JSON (captures whatever the request carrier holds)
//  3. a minimal fallback built from the task ID
func taskEventText(task *models.Task) string {
	if task == nil {
		return ""
	}
	if task.Payload != nil {
		for _, key := range []string{"request", "query", "input", "prompt", "instruction"} {
			if s, ok := task.Payload[key].(string); ok && strings.TrimSpace(s) != "" {
				return truncateRunes(s, maxEventTextFieldLen)
			}
		}
		if b, err := json.Marshal(task.Payload); err == nil && len(b) > 0 {
			return truncateRunes(string(b), maxEventTextFieldLen)
		}
	}
	return truncateRunes(task.TaskID, maxEventTextFieldLen)
}

// resultEventText extracts a best-effort textual description of a task result.
// Preference order: the reason, then recommendation item content/descriptions,
// then the result metadata JSON. Returns "" when the result is nil.
func resultEventText(result *models.TaskResult) string {
	if result == nil {
		return ""
	}
	var sb strings.Builder
	if result.Reason != "" {
		sb.WriteString(result.Reason)
		sb.WriteString("\n")
	}
	for _, item := range result.Items {
		if item == nil {
			continue
		}
		switch {
		case item.Content != "":
			sb.WriteString(item.Content)
			sb.WriteString("\n")
		case item.Description != "":
			sb.WriteString(item.Description)
			sb.WriteString("\n")
		case item.Name != "":
			sb.WriteString(item.Name)
			sb.WriteString("\n")
		}
	}
	if sb.Len() == 0 && len(result.Metadata) > 0 {
		if b, err := json.Marshal(result.Metadata); err == nil {
			sb.WriteString(string(b))
		}
	}
	return truncateRunes(sb.String(), maxEventTextFieldLen)
}

// truncateRunes safely truncates s to at most max runes (not bytes) so we never
// split a multi-byte UTF-8 character, which would corrupt JSON event payloads.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
