# Schema files

Each file describes one version of one mioty interface: every operation, which side
starts it, and every field of its messages with type and presence. simple-sc and
simple-ac check every message they send and receive against these files, and build their
own messages from them. A new version of an interface is mostly a new file here.

The files are built into the programs. To try a changed or new version without
rebuilding, put a file into the schema folder of simple-sc (`data/schemas`, or
`SSCAC_SCHEMA_DIR`). A file there replaces the built-in file of the same interface and
version, or adds a new version.

| File | Interface | Version |
|---|---|---|
| `bssci-1.0.yaml` | BSSCI, base station to Service Center | 1.0.0 rev1 |
| `bssci-1.1.yaml` | BSSCI | 1.1.0 rev2, draft |
| `bssci-vm-1.1.yaml` | BSSCI attachment Variable MAC | 1.1.0, draft |
| `scaci-1.1.yaml` | SCACI, Service Center to Application Center | 1.1.0, draft |

The files are derived from the mioty specifications, which are published by the
[mioty Alliance](https://mioty-alliance.com). The specifications are the reference: if a
file and the specification disagree, the file is wrong, please open an issue.

## Format

```yaml
interface: bssci            # bssci, bssci-vm, scaci
version: "1.0.0"
identifier: MIOTYB01        # identifier in the frame header
roles: {initiator_positive: bs, initiator_negative: sc}

core:                       # fields in every message
  command: {type: string}
  opId:    {type: integer, bits: 64, signed: true}

types:                      # object types used by fields
  subpackets:
    fields: {snr: {type: array, items: number}, ...}

operations:
  att:                      # command of the first message
    title: Attach
    clause: "5.6"           # clause in the specification
    initiator: bs           # bs, sc, ac or both
    error_conditions:       # errors the specification lists
      - {part: init, code: EPERM, reason: "End Point signature invalid"}
    init:     {<field>: <field spec>, ...}   # message "att"
    response: {...}                          # message "attRsp"
    complete: {}                             # message "attCmp"

errors:
  error:    {code: ..., message: ...}       # may replace a response or complete message
  errorAck: {}
```

The three messages of an operation are named after the command: `<cmd>`, `<cmd>Rsp`,
`<cmd>Cmp`. The core fields are not repeated per message.

### Field spec

| Key | Meaning |
|---|---|
| `type` | `string`, `bool`, `object`, `integer`, `number` (integer or float), `eui64`, `bytes` (array of byte values), `array`, `enum`, `any`, or the name of an entry in `types` |
| `spec` | The type exactly as the specification writes it |
| `presence` | `required` (default), `optional` or `conditional` |
| `when` | For `conditional`: the condition in words |
| `default` | Value to assume when an optional field is missing |
| `default_from` | Default taken from another field, e.g. `init.version` |
| `bits`, `signed` | Integer width and sign, where the specification states them |
| `min`, `max` | Value range |
| `len` | Exact length of `bytes` or `array` |
| `items` | Element type of an `array` |
| `values` | Allowed values of an `enum` |
| `format` | `semver` for version strings |
| `unit` | Unit of a number, for reading only |
| `secret` | Key material: never written to logs or traces |
| `note` | Remark, for example where the specification leaves a width open |

### Rules the programs apply

- Unknown fields in a received message are ignored, missing optional fields get their
  default, missing required fields are an error.
- Messages the programs send contain every required field and no field the file does not
  list.
- EUIs and 64 bit integers stay exact, they are never converted to floating point.
