#!/usr/bin/env bash
# G2 config contract gate (ares-repair-plan-zh.md §8): every leaf field of
# ares_config must be consumed outside the config package itself (validate /
# defaults / redacted don't count — they are self-referential). Fields with no
# consumer make the YAML a lie; wire them or document them as display-only.
set -euo pipefail

cd "$(dirname "$0")/.."

# Known display-only / validated-only fields (documented in config.go, C4).
WHITELIST=(
  "ServerConfig.Host"          # display-only (C4)
  "KernelConfig.Policy"        # display-only (C4)
  "MemoryConfig.EnableDistillation"  # wired in wireDistillation (C1)
  "MemoryConfig.DistillationThreshold" # validated + defaulted; gates nothing yet
  "ToolsConfig.Defaults"
  "ToolsConfig.Agents"
  "MemoryConfig.SessionMemory"
  "MemoryConfig.UserProfile"
  "MemoryConfig.TaskDistillation"
  # C4 verified dead leaves — removal pending (Phase 5 C4), whitelisted so the
  # gate catches NEW dead fields rather than the known backlog:
  "WorkflowConfig.AutoReload"
  "WorkflowConfig.DefinitionPath"
  "WorkflowConfig.ReloadInterval"
  "ValidationConfig.CustomSchema"
  "OutputConfig.ItemSchema"
  "OutputConfig.ResultSchema"
  "PromptsConfig.ProfileExtraction"
  "PromptsConfig.StyleAnalysis"
  "StorageConfig.PGVector"
  "EmbeddingConfig.RedisAddr"
  "KnowledgeConfig.Triggers"
  "WorkflowConfig.ItemTemplate"
  "WorkflowConfig.SummaryTemplate"
  "WorkflowConfig.SystemPrompt"
  "WorkflowConfig.TableName"
  "KnowledgeConfig.VectorDB"
  "MemoryConfig.Workflow"
)

# Collect all exported field names on config structs.
fields=$(grep -E '^\s+[A-Z][A-Za-z0-9]+\s+.*`yaml:"' internal/ares_config/config.go | awk '{print $1}' | sort -u)

dead=()
for f in $fields; do
  skip=false
  for w in "${WHITELIST[@]}"; do
    if [[ "$w" == *".$f" ]]; then skip=true; break; fi
  done
  if [[ "$skip" == true ]]; then continue; fi
  # Consumer: a `.Field` read outside ares_config, excluding tests.
  count=$( { grep -rn "\.$f\b" --include="*.go" internal/ cmd/ sdk/ api/ services/ 2>/dev/null \
    | grep -v "_test.go" | grep -v "internal/ares_config/" || true; } | wc -l | tr -d ' ')
  if [ "$count" -eq 0 ]; then
    dead+=("$f")
  fi
done

if [ ${#dead[@]} -eq 0 ]; then
  echo "G2 config contract gate: PASSED (no undocumented dead fields)"
  exit 0
fi

echo "G2 config contract gate: FAILED — fields with zero external consumers (wire them, remove them, or whitelist with a reason):"
for p in "${dead[@]}"; do
  echo "  $p"
done
exit 1
