---
title: zkVM Overview
tags: [zkp, zkvm, proving-system]
---

# zkVM Overview

A zero-knowledge virtual machine (zkVM) executes a program and produces a
succinct proof that the execution was correct, without revealing the private
inputs. #zkvm

## Execution Model

The zkVM runs a guest program compiled to a supported instruction set. Every
executed instruction is recorded into an execution trace, which is later
arithmetized into polynomial constraints.

### Trace Columns

The trace is a table where each row is one machine cycle and each column is a
register or auxiliary value.

| column      | meaning                        |
| ----------- | ------------------------------ |
| pc          | program counter                |
| opcode      | decoded instruction opcode     |
| rs1, rs2    | source register values         |
| rd          | destination register value     |

### Constraint System

Constraints fall into a few families:

- transition constraints tie row `i` to row `i+1`
- boundary constraints pin the first and last rows
- lookup constraints validate range checks and memory access

## Proving Pipeline

The prover walks the trace, commits to the columns, and runs a polynomial
IOP such as STARK or PLONK. A typical Rust entrypoint looks like:

```rust
fn prove(program: &Program, input: &[u8]) -> Proof {
    let trace = execute(program, input);
    let air = build_air(&trace);
    stark::prove(&air, &trace)
}
```

The resulting proof is verified in milliseconds regardless of program length.
