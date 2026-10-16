// This file implements retrieval-augmented question answering over the stored
// knowledge base: retrieve the most relevant chunks, assemble a grounded
// prompt, and generate an answer. Without an LLM it degrades to returning the
// retrieved passages directly.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	areserrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// snippetRunes bounds how much of a chunk is shown as a citation snippet.
const snippetRunes = 240

// Source is a single retrieved passage used to ground an answer.
type Source struct {
	Rank    int     // One-based citation index.
	Score   float64 // Similarity score in [0, 1].
	Path    string  // Source file path.
	Snippet string  // Truncated content preview.
}

// Answer is the result of a RAG query.
type Answer struct {
	Question  string   // The original question.
	Text      string   // The generated or fallback answer text.
	Sources   []Source // Retrieved passages, in rank order.
	Generated bool     // True when produced by the LLM, false for fallbacks.
}

// Ask answers a question using retrieval-augmented generation.
//
// Args:
//
//	ctx      - cancellation context.
//	tenantID - tenant namespace to search, must be non-empty.
//	question - the user question, must be non-empty.
//
// Returns:
//
//	answer - the answer with cited sources, never nil on success.
//	err    - a retrieval or generation error with context.
func (kb *KnowledgeBase) Ask(ctx context.Context, tenantID, question string) (*Answer, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, wrapf(os.ErrInvalid, "ask: empty tenant id")
	}
	if strings.TrimSpace(question) == "" {
		return nil, wrapf(os.ErrInvalid, "ask: empty question")
	}

	// The PRODUCTION retrieval path: embed the question (with the configured query
	// prefix — that is how this corpus was indexed) and search the same
	// knowledge_chunks table the import wrote, through the same repository method the
	// server uses. This replaced an example-only retrieval service that carried its own
	// scoring: two rankings for one corpus, and the example was exercising the one
	// nothing else ran (see the deprecated services in internal/storage/postgres).
	queryVector, err := kb.embedder.EmbedWithPrefix(ctx, question, kb.cfg.Knowledge.QueryPrefix)
	if err != nil {
		return nil, wrapf(err, "embed question")
	}
	chunks, err := kb.repo.SearchByVector(ctx, queryVector, tenantID, kb.cfg.Knowledge.TopK)
	if err != nil {
		// The repository reports "nothing matched" as ErrRecordNotFound rather than an
		// empty slice, so an empty knowledge base is a normal outcome here, not a failure.
		if !errors.Is(err, areserrors.ErrRecordNotFound) {
			return nil, wrapf(err, "retrieve for question")
		}
		chunks = nil
	}
	chunks = filterByMinScore(chunks, kb.cfg.Knowledge.MinScore)

	sources := toSources(chunks)
	if len(sources) == 0 {
		return &Answer{
			Question:  question,
			Text:      "No relevant information was found in the knowledge base.",
			Generated: false,
		}, nil
	}

	if kb.llmClient == nil {
		return &Answer{
			Question:  question,
			Text:      retrievalOnlyAnswer(sources),
			Sources:   sources,
			Generated: false,
		}, nil
	}

	prompt := buildPrompt(question, chunks)
	text, err := kb.llmClient.Generate(ctx, prompt)
	if err != nil {
		return nil, wrapf(err, "generate answer")
	}
	return &Answer{
		Question:  question,
		Text:      strings.TrimSpace(text),
		Sources:   sources,
		Generated: true,
	}, nil
}

// chunkSimilarity reads the cosine similarity the repository stamps into a chunk's
// metadata. It is 0 when the field is absent, which makes a chunk without a score fail
// any positive MinScore rather than silently pass it.
func chunkSimilarity(chunk *models.KnowledgeChunk) float64 {
	if chunk == nil || chunk.Metadata == nil {
		return 0
	}
	if v, ok := chunk.Metadata["similarity"].(float64); ok {
		return v
	}
	return 0
}

// filterByMinScore keeps the chunks that clear the configured similarity floor.
//
// The floor lives here rather than in the repository because SearchByVector returns
// whatever the recall window matched — it takes no MinScore (its own SQL predicate is the
// embedding-status filter). Keeping the check in the caller preserves the behaviour the
// old example service had, without a second scoring implementation.
func filterByMinScore(chunks []*models.KnowledgeChunk, minScore float64) []*models.KnowledgeChunk {
	if minScore <= 0 {
		return chunks
	}
	kept := make([]*models.KnowledgeChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		if chunkSimilarity(chunk) >= minScore {
			kept = append(kept, chunk)
		}
	}
	return kept
}

// toSources converts retrieved chunks into display-ready sources.
func toSources(chunks []*models.KnowledgeChunk) []Source {
	sources := make([]Source, 0, len(chunks))
	for i, chunk := range chunks {
		if chunk == nil {
			continue
		}
		sources = append(sources, Source{
			Rank:    i + 1,
			Score:   chunkSimilarity(chunk),
			Path:    chunk.Source,
			Snippet: truncateRunes(strings.TrimSpace(chunk.Content), snippetRunes),
		})
	}
	return sources
}

// retrievalOnlyAnswer joins the retrieved passages when no LLM is configured.
func retrievalOnlyAnswer(sources []Source) string {
	var b strings.Builder
	b.WriteString("Retrieval-only mode (no LLM configured). Top passages:\n")
	for _, s := range sources {
		fmt.Fprintf(&b, "\n[%d] (score %.3f) %s\n%s\n", s.Rank, s.Score, s.Path, s.Snippet)
	}
	return b.String()
}

// buildPrompt assembles a grounded prompt instructing the model to answer only
// from the provided context and to cite passages by their bracketed index.
func buildPrompt(question string, chunks []*models.KnowledgeChunk) string {
	var b strings.Builder
	b.WriteString("You are a knowledge base assistant. Answer the question using ")
	b.WriteString("only the numbered context below. Cite sources as [n]. ")
	b.WriteString("If the context is insufficient, say so explicitly.\n\n")
	b.WriteString("Context:\n")
	for i, chunk := range chunks {
		if chunk == nil {
			continue
		}
		fmt.Fprintf(&b, "[%d] source: %s\n%s\n\n", i+1, chunk.Source, strings.TrimSpace(chunk.Content))
	}
	b.WriteString("Question: ")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n\nAnswer:")
	return b.String()
}
