// Package compiler — PromptSelector picks the nodes that should enter the LLM
// prompt context: decisions, constraints, goals, tradeoffs, open questions,
// recent facts, and memory nodes. It is token-budget aware: it estimates
// tokens (~4 chars/token) and truncates the lowest-score nodes once the
// budget is reached. Zero LLM calls.
package compiler

import "sort"

// charsPerToken is the rough conversion ratio used to estimate token cost
// from a node's textual description. LLM tokenizers average ~4 chars/token
// across English and code content; this is intentionally a heuristic.
const charsPerToken = 4

// PromptSelector picks nodes that should enter the LLM prompt context.
//
// Candidate types (in default priority order): Decision, Constraint, Goal,
// Tradeoff, Question, Fact, Memory. Entity and Reference are excluded — they
// are structural and belong to the AKG, not the prompt.
//
// Scoring: TypePriority[type] * Confidence * (1 + AccessCount*0.1).
// Nodes are sorted descending and greedily added until the token budget or
// node cap is reached.
type PromptSelector struct {
	// MaxTokens is the token budget for the selected context. Zero means no cap.
	MaxTokens int

	// MaxNodes is the maximum number of nodes to return. Zero means no cap.
	MaxNodes int

	// TypePriority maps node types to their prompt priority. Missing types
	// default to 1.0.
	TypePriority map[NodeType]float64
}

// NewPromptSelector creates a PromptSelector with default type priorities.
//
// Default priority tiers:
//   - Decision, Constraint: 3.0 (drives next-step reasoning)
//   - Goal, Tradeoff: 2.0
//   - Question, Fact: 1.5
//   - Memory: 1.2 (distilled context, useful but secondary)
func NewPromptSelector(maxTokens, maxNodes int) *PromptSelector {
	return &PromptSelector{
		MaxTokens: maxTokens,
		MaxNodes:  maxNodes,
		TypePriority: map[NodeType]float64{
			NodeDecision:   3.0,
			NodeConstraint: 3.0,
			NodeGoal:       2.0,
			NodeTradeoff:   2.0,
			NodeQuestion:   1.5,
			NodeFact:       1.5,
			NodeMemory:     1.2,
		},
	}
}

// Select returns a SubGraph of prompt-relevant nodes from the KnowledgeModel.
// Returns a non-nil empty SubGraph for nil input.
func (s *PromptSelector) Select(km *KnowledgeModel) *SubGraph {
	if km == nil {
		return &SubGraph{Metadata: map[string]any{attrSelector: "prompt", "tokens_estimated": 0, attrNodesSelected: 0}}
	}

	candidates := s.collectCandidates(km)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].node.ID < candidates[j].node.ID
	})

	selected, tokensEstimated := s.applyBudget(candidates)
	return buildSubGraph(km, candidatesToNodes(selected), map[string]any{
		"selector":         "prompt",
		"tokens_estimated": tokensEstimated,
		attrNodesSelected:  len(selected),
	})
}

// Name returns the selector's identifier.
func (s *PromptSelector) Name() string { return "prompt" }

// collectCandidates gathers every node whose Type has a non-zero priority in
// s.TypePriority and scores it. Nodes with zero priority (e.g. Entity,
// Reference) are skipped.
func (s *PromptSelector) collectCandidates(km *KnowledgeModel) []scoredCandidate {
	var out []scoredCandidate
	for _, n := range km.Nodes {
		if n == nil {
			continue
		}
		priority, ok := s.TypePriority[n.Type]
		if !ok || priority <= 0 {
			continue
		}
		out = append(out, scoredCandidate{node: n, score: s.scoreNode(n, priority)})
	}
	return out
}

// scoreNode computes the prompt score for a node given its type priority.
// score = priority * Confidence * (1 + AccessCount * 0.1).
func (s *PromptSelector) scoreNode(n *Node, priority float64) float64 {
	if n == nil {
		return 0
	}
	accessFactor := 1.0 + float64(n.AccessCount)*0.1
	return priority * n.Confidence * accessFactor
}

// applyBudget greedily adds candidates while staying within the MaxTokens and
// MaxNodes limits. It returns the selected candidates and the total estimated
// token count.
func (s *PromptSelector) applyBudget(candidates []scoredCandidate) ([]scoredCandidate, int) {
	selected := make([]scoredCandidate, 0, len(candidates))
	tokens := 0
	for _, c := range candidates {
		if s.MaxNodes > 0 && len(selected) >= s.MaxNodes {
			break
		}
		cost := estimateTokens(c.node)
		if s.MaxTokens > 0 && tokens+cost > s.MaxTokens {
			break
		}
		selected = append(selected, c)
		tokens += cost
	}
	return selected, tokens
}

// estimateTokens returns a rough token-cost estimate for a node based on the
// length of its description attribute (and falls back to the ID when no
// description is present). Uses ~4 chars/token.
func estimateTokens(n *Node) int {
	if n == nil {
		return 0
	}
	text := nodeDescription(n)
	if len(text) == 0 {
		// ID alone is still a token or two; assume 1 so empty nodes don't
		// blow the budget silently.
		return 1
	}
	return (len(text) + charsPerToken - 1) / charsPerToken
}

// nodeDescription extracts a textual representation of a node for token
// estimation. It prefers an explicit "description"/"text"/"choice"/"name"
// attribute, then falls back to the ID.
func nodeDescription(n *Node) string {
	if n == nil || n.Attributes == nil {
		if n != nil {
			return n.ID
		}
		return ""
	}
	for _, key := range []string{attrDescription, "text", attrChoice, attrName} {
		if v, ok := n.Attributes[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return n.ID
}
