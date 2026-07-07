package knowledge

// Relation connects two KnowledgeObjects with a named relationship.
// Name is a string (not a const enum) so users can register custom
// relation types like "worked_with", "managed_by", "friend_of".
type Relation struct {
	From       string         `json:"from"` // KnowledgeObject ID
	To         string         `json:"to"`   // KnowledgeObject ID
	Name       string         `json:"name"` // Relationship name
	Properties map[string]any `json:"properties,omitempty"`
	Score      float64        `json:"score"` // Strength [0, 1]
	Evidence   string         `json:"evidence,omitempty"`
}

// Built-in relation names.
const (
	RelDependsOn   = "depends_on"
	RelCalls       = "calls"
	RelCauses      = "causes"
	RelFixes       = "fixes"
	RelBelongsTo   = "belongs_to"
	RelUses        = "uses"
	RelImplements  = "implements"
	RelSimilarTo   = "similar_to"
	RelGeneratedBy = "generated_by"
	RelDecidedBy   = "decided_by"
	RelSupersedes  = "supersedes"
	RelLearnsFrom  = "learns_from"
)

// WorkingGraph is a task-specific cognitive graph.
// Lifecycle: Build → Consume → Destroy. Never persisted.
type WorkingGraph struct {
	Nodes map[string]*KnowledgeObject `json:"nodes"`
	Edges []Relation                  `json:"edges"`
}
