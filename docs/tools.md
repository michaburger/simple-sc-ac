# Tools

## sim-bs, a simulated base station

`sim-bs` connects to simple-sc like a real base station. It attaches simulated
end-points with real signatures and sends uplinks, so you can try simple-sc, or test your
own changes, without hardware.

```sh
./bin/sim-bs -ep 02-00-00-00-00-00-00-01:000102030405060708090A0B0C0D0E0F -uplinks 3
```

| Option | Default | Meaning |
|---|---|---|
| `-sc` | `localhost:16107` | Address of simple-sc |
| `-server-name` | host of `-sc` | Name to check in the server certificate |
| `-data` | `data` | Data folder of a simple-sc: `sim-bs` takes the CA from there and issues itself a certificate |
| `-schemas` | `<data>/schemas` | Schema files that replace or add to the built-in ones |
| `-eui` | `02-00-00-00-00-00-0B-01` | EUI-64 of the simulated base station |
| `-version` | `1.1.0` | BSSCI version to ask for |
| `-ep` | | Simulated end-point as `EUI:KEY`, repeat for several |
| `-uplinks` | `1` | Uplinks per end-point |
| `-interval` | `1s` | Time between uplinks |
| `-detach` | off | Detach the end-points at the end |

The end-points have to be added to simple-sc first, with the same EUI and key. Use keys
you make up, never the key of a real device.

Things to try:

- `-version 1.0.0`, to see a BSSCI 1.0 session.
- Two `sim-bs` with different `-eui` at the same time: an uplink both send is stored once.
- `-version 2.0.0`: simple-sc refuses the version.
- With the default `pending` mode, a new `-eui` gets no attach until you accept it.

## trace-view, the message trace

simple-sc writes every message it sends and receives to `data/trace/trace.jsonl`, one
JSON object per line, with keys replaced by `<redacted>`. `trace-view` prints it so you
can read it:

```sh
./bin/trace-view data/trace/trace.jsonl
```

| Option | Meaning |
|---|---|
| `-f` | Follow the file, like `tail -f` |
| `-conn TEXT` | Only connections whose name contains TEXT, e.g. a base station EUI |
| `-cmd TEXT` | Only commands starting with TEXT, e.g. `att` for attach messages |
| `-full` | Every field, not shortened |

```sh
./bin/trace-view -f -cmd att data/trace/trace.jsonl
```

`<-` is a message simple-sc received, `->` one it sent. A line with `finding:` below it
is a message that did not match its schema exactly: useful when a base station behaves
differently from what you expect.
