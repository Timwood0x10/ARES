# GoAgentX Autonomous Evolution (Dream Mode v1) Demo

## Overview

This demo showcases the **Autonomous Evolution System** of GoAgentX, also known as **Dream Mode v1**. It demonstrates how agents can self-improve through a closed-loop evolution process that includes feedback collection, strategy mutation, arena testing, and genealogy tracking.

## Prerequisites

- **Go**: 1.26.1 or later
- **Module**: `goagentx` (this project)

## How to Run

```bash
# Navigate to the demo directory
cd examples/autonomous-evolution/

# Run the demo
go run main.go
```

No external services (LLM, database, etc.) are required. All dependencies use in-memory mock implementations.

## Demo Scenarios

### Scenario 1: Bandit Feedback Loop

Demonstrates the experience reinforcement mechanism:

- Creates an in-memory Experience Repository with sample experiences
- Simulates task executions (success/failure)
- Shows how successful usage increments `UsageCount`
- Shows how failures trigger `DecrementRank` (score reduction by ~10%)
- Displays final ranking state showing quality optimization

**Key takeaway**: The bandit algorithm reinforces reliable patterns and penalizes failing ones, enabling continuous experience quality improvement.

### Scenario 2: Callback Event System

Demonstrates the lifecycle event hook system:

- Registers custom handlers for LLM/Tool/Agent events
- Emits simulated events (start, end, error)
- Verifies handlers capture all events with metadata
- Shows panic-safe handler execution

**Supported event types**:
- `llm.start`, `llm.end`, `llm.error`, `llm.token`
- `agent.start`, `agent.end`, `agent.error`
- `tool.start`, `tool.end`, `tool.error`

**Key takeaway**: The callback system enables observability, metrics collection, and debugging hooks for all agent operations.

### Scenario 3: Strategy Mutation Engine

Demonstrates the strategy variation generator:

- Defines a parent strategy with parameters (temperature, top_k, etc.)
- Generates N child strategies with parameter mutations
- Shows deterministic behavior (same seed → same results)
- Displays mutation type distribution (80% parameter, 20% prompt)

**Mutable parameters**:
| Parameter | Values |
|-----------|--------|
| temperature | 0.1, 0.3, 0.5, 0.7, 0.9 |
| top_k | 10, 20, 40, 80 |
| max_steps | 5, 10, 15, 20 |
| memory_limit | 3, 5, 10 |
| conflict_threshold | 0.85, 0.90, 0.95 |

**Key takeaway**: Systematic exploration of strategy space with reproducible mutations.

### Scenario 4: Arena Regression Test

Demonstrates A/B style comparison with statistical analysis:

- Compares two strategies (baseline vs candidate)
- Runs multiple trials per strategy
- Computes win rate and statistical significance (Welch's t-test)
- Reports p-value for confidence assessment

**Output metrics**:
- Average scores per strategy
- Win rate (fraction where candidate >= baseline)
- Statistical significance (p < 0.05)
- Individual run scores with comparison markers

**Key takeaway**: Data-driven strategy adoption decisions backed by statistical rigor.

### Scenario 5: Dream Cycle Orchestration

Demonstrates the complete autonomous evolution loop:

```
Trigger → Mutate(N candidates) → Arena Test → Select Best → Genealogy Record
```

1. **Mutation**: Generate 3 candidate strategies from parent
2. **Arena Testing**: Compare each candidate against baseline
3. **Selection**: Pick best candidate above win rate threshold (0.55)
4. **Genealogy**: Record lineage (parent → child relationship)

**Key takeaway**: End-to-end autonomous self-improvement without human intervention.

## Expected Output

```
╔══════════════════════════════════════════════════════════════╗
║     GoAgentX Autonomous Evolution (Dream Mode v1) Demo       ║
╚══════════════════════════════════════════════════════════════╝

============================================================
  Scenario 1: Bandit Feedback Loop
============================================================
...

============================================================
  Scenario 2: Callback Event System
============================================================
...

============================================================
  Scenario 3: Strategy Mutation Engine
============================================================
...

============================================================
  Scenario 4: Arena Regression Test
============================================================
...

============================================================
  Scenario 5: Dream Cycle Orchestration
============================================================
...

============================================================
Demo Complete
============================================================

All 5 scenarios executed successfully!
```

## File Structure

```
examples/autonomous-evolution/
├── main.go              # Main entry point with 5 demo scenarios
├── config/
│   └── config.yaml      # Configuration file (for reference)
└── README.md            # This documentation
```

## Architecture Components

| Component | Package | Role |
|-----------|---------|------|
| FeedbackService | `internal/experience` | Bandit reinforcement via success/failure |
| CallbackRegistry | `internal/callbacks` | Lifecycle event pub/sub system |
| Mutator | `internal/evolution/mutation` | Strategy variant generator |
| RegressionTester | `internal/arena` | A/B comparison with statistics |
| DreamCycle | `internal/evolution` | Full orchestration loop |

## Notes

- All mock implementations are defined inline in `main.go`
- No network calls or database connections required
- Deterministic output when using fixed random seeds
- Suitable for CI/CD pipelines and development environments
