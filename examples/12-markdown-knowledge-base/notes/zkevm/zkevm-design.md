---
title: zkEVM Design Notes
tags: [zkp, zkevm, ethereum]
---

# zkEVM Design Notes

A zkEVM proves correct execution of EVM bytecode. It aims for equivalence with
Ethereum so existing contracts run unmodified. #zkevm #ethereum

## Equivalence Levels

There is a well-known spectrum of equivalence, trading proving cost against
compatibility:

1. language-level equivalence (compile Solidity to a custom VM)
2. bytecode-level equivalence (run EVM opcodes, tweak a few)
3. consensus-level equivalence (bit-for-bit identical to Ethereum)

## Circuit Architecture

The proof is split into interacting sub-circuits connected by lookup arguments.

### Main Circuits

- the EVM circuit constrains opcode execution step by step
- the state circuit constrains storage and account reads/writes
- the bytecode circuit binds contract code to its keccak hash

### Gas Accounting

Every opcode deducts gas. The circuit must reproduce gas semantics exactly,
including out-of-gas reverts:

```solidity
function transfer(address to, uint256 amount) external {
    require(balanceOf[msg.sender] >= amount, "insufficient");
    balanceOf[msg.sender] -= amount;
    balanceOf[to] += amount;
}
```

## Open Problems

Keccak and modular exponentiation precompiles remain expensive to prove, and
are active areas of optimization.
