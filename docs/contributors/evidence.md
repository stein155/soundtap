# Reverse-engineering evidence

Keep observations separate from decisions and operational instructions. A stock binary is
an input to analysis, not a specification of every hardware state or proof that recovery works.

- **Static:** bytes, register writes, strings or control flow read from an identified artifact.
- **Inferred:** an explanation consistent with those observations, with alternatives and
  limits stated. A symbol name alone does not prove which execution path ran.
- **Measured offline:** output from an emulator or fake device, with tool and input identity;
  it does not prove physical electrical behaviour.
- **Measured on hardware:** an observation on a named model/board, with firmware/build, date, procedure
  and captured result. A USB ROM enumeration is not a completed NAND restore.
- **Candidate-approved:** the exact artifact passed the required per-model hardware gates;
  record the build and generated artifact hashes. A previous model or build cannot supply it.

Record the source firmware version, file path and SHA-256, relevant addresses/registers,
analysis method and evidence location. Source hashes identify RE inputs; release hashes
come from generated `SHA256SUMS`. Keep stock files outside git. If an old note lacks a hash
or capture, say so instead of inventing one or upgrading the claim to “proven”.

When a conclusion changes, keep the useful measurements and explain their limits, but remove
obsolete commands from current runbooks. Current implementation belongs in the [design](design.md) and the [ADRs](../decisions/README.md);
permission rules belong in [the contract](../../CONTRIBUTING.md#the-contract). Link to those authorities rather than duplicating them.
