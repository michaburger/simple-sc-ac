# simple-sc-ac: notes for coding agents

Open source mioty Service Center (`simple-sc`) and Application Center (`simple-ac`) in Go.

## Rules

1. Language of all files: English. User documentation in short sentences, second person.
2. Keep it simple. The README must stay clear for someone new to the project: what it is,
   how to run it, how to connect a base station. Update it with every change to a command,
   a port or a configuration key, and run every command in it before committing.
3. Go: standard library first, few dependencies, pure Go only (no cgo), static binaries for
   `amd64`, `arm64` and Windows.
4. Messages are generic maps validated by the schema files in `schemas/`, no struct per
   message. Message definitions live in the schema files, not in code. The files are
   built into the programs, a schema folder can replace or add versions.
5. SC and AC stay separable: no import from `internal/sc` into `internal/ac` or the other
   way. Only `internal/proto` and `internal/common` are shared.
6. BSSCI 1.0 and 1.1 are both first class. Never send an operation that the version of the
   session does not define.
7. Containers: all state under `/data`, every configuration key settable by environment
   variable, the first start works without a configuration file.
8. No real key material in code, tests or examples. Test vectors use generated keys.
9. Field names and types come from the interface specifications, never from memory.
10. This repository contains code, schema files, tests, deployment files and user
    documentation, everything needed to run it. No planning notes, decision logs,
    copies of specifications or lab details. Specifications are linked, not copied.
