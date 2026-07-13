# 12 · Markdown Knowledge Base (structure-aware + RAG)

A self-contained example that turns a directory of markdown notes into a
queryable knowledge base backed by **PostgreSQL + pgvector**, and answers
questions over it with retrieval-augmented generation (RAG).

It is independent from `11-knowledge-import`: instead of driving ingestion
through an LLM agent/tool loop, this example implements a direct, structure-first
pipeline with a richer markdown parser and metadata model.

## What it does

Given a directory (e.g. `~/Documents/notes/zkp/`):

1. **Recursive discovery** — finds every `.md` / `.markdown` file.
2. **Structure-aware parsing** — classifies content into typed blocks:
   headings (`#`/`##`/`###`), fenced code blocks, GFM tables, lists, and
   blockquotes. Code blocks and tables are kept intact.
3. **Section-first chunking** — chunks follow headings, *not* a fixed character
   window. A section is split only when it exceeds `chunk_size`, and never in the
   middle of a code block or table. Continuation chunks repeat the heading
   breadcrumb so each chunk stays self-contained.
4. **Metadata extraction** — title (frontmatter / first H1 / filename), heading
   breadcrumb, heading level, block types, and tags (frontmatter `tags:` plus
   inline `#hashtags`).
5. **Vectorize + store** — embeds each chunk (asymmetric `passage:` prefix),
   L2-normalizes, and writes to `knowledge_chunks_1024`.
6. **RAG Q&A** — retrieves the top-K chunks by cosine similarity, builds a
   grounded prompt, and generates a cited answer. Falls back to retrieval-only
   output when no LLM is configured.

## Layout

| file         | responsibility                                        |
| ------------ | ----------------------------------------------------- |
| `config.go`  | config model, loading, defaults, startup validation   |
| `parser.go`  | frontmatter, title, tags, document assembly           |
| `scanner.go` | line-oriented block scanner (headings/code/table/…)   |
| `chunker.go` | section splitting, size-bounded chunking, metadata     |
| `store.go`   | pool/schema/embedding/repo/retrieval wiring + ingest  |
| `rag.go`     | retrieval-augmented answering                          |
| `main.go`    | CLI + graceful shutdown                                |

## Prerequisites

- A PostgreSQL instance with the `pgvector` extension available (the example
  runs `CREATE EXTENSION IF NOT EXISTS vector` and applies the shared schema).
- An embedding service compatible with the client in
  `internal/storage/postgres/embedding` (Ollama-style), serving a **1024-dim**
  model such as `qwen3-embedding:0.6b`.
- Optional: an LLM endpoint (e.g. Ollama `llama3.2`) for answer generation.

Adjust `config.yaml` to match your database, embedding, and LLM endpoints. The
`embedding.dimensions` value must equal the `VECTOR(1024)` column dimension.

## Run

```bash
# ingest the bundled sample notes
go run ./examples/12-markdown-knowledge-base --dir examples/12-markdown-knowledge-base/notes

# ingest your own directory
go run ./examples/12-markdown-knowledge-base --dir /path/to/notes --tenant myteam

# ask a question (RAG)
go run ./examples/12-markdown-knowledge-base --ask "How is gas accounted for in a zkEVM?" --tenant myteam

# list stored documents
go run ./examples/12-markdown-knowledge-base --list --tenant myteam
```

Re-ingesting the same content is idempotent: chunks are keyed by a SHA-256
content hash, so unchanged sections are not duplicated.
