package experience

import "github.com/Timwood0x10/ares/internal/llmexp"

// ExperienceRepository defines the interface for experience storage and
// retrieval. It is the storage-agnostic contract that decouples the
// distillation pipeline from any specific vector database.
type ExperienceRepository = llmexp.ExperienceRepository
