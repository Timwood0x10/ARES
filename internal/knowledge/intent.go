package knowledge

// Intent describes what knowledge is needed and within what constraints.
// The KnowledgePlanner consumes Intent to generate a KnowledgePlan.
type Intent struct {
	Goal        string       `json:"goal"`                  // Natural language goal
	Scope       Scope        `json:"scope"`                 // Scope constraints
	Constraints []Constraint `json:"constraints,omitempty"` // Additional constraints
	Budget      TokenBudget  `json:"budget"`                // Token budget
}

// Scope defines the boundaries for knowledge retrieval.
type Scope struct {
	Namespaces []string     `json:"namespaces,omitempty"`
	Types      []ObjectType `json:"types,omitempty"`
	MaxObjects int          `json:"max_objects"`
}

// Constraint is a key-value filter with an operator.
type Constraint struct {
	Key   string `json:"key"`
	Op    string `json:"op"` // eq / neq / gt / lt / in / contains
	Value any    `json:"value"`
}

// TokenBudget allocates token usage between graph context and LLM reasoning.
type TokenBudget struct {
	MaxTokens int `json:"max_tokens"` // Total token budget
	Reserved  int `json:"reserved"`   // Reserved for LLM reasoning
	ForGraph  int `json:"for_graph"`  // Allocated for graph context
}
