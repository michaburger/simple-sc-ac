# Getting started

From nothing to a first uplink, on one computer, without hardware. A simulated base
station stands in for a real one. Takes about ten minutes.

You need the programs from the [installation guide](installation.md). The commands are for a Linux or macOS shell. On Windows use the same
commands in Git Bash, or replace `./bin/simple-sc` with `.\bin\simple-sc.exe` in
PowerShell.

## 1. Start simple-sc

For this first test, let it accept any base station with a valid certificate:

```sh
SSCAC_BSSCI_UNKNOWN_BASE_STATIONS=accept ./bin/simple-sc
```

At the first start simple-sc creates the `data` folder, gives itself an EUI and a lab
certificate authority, and starts listening:

```
... "msg":"identity" ... "eui":"02-E0-C5-D4-B9-87-00-8C","source":"derived from a hardware MAC address"
... "msg":"lab certificate authority created" ...
... "msg":"BSSCI listening","addr":":16107","versions":["1.0.0","1.1.0"],"unknown_base_stations":"accept"
... "msg":"admin REST listening","addr":"127.0.0.1:8107"
```

Leave it running and open a second terminal for the next steps.

## 2. Add an end-point

An end-point is known by its EUI-64 and its network key. For a simulated end-point, make
up both. Never use the key of a real device in a test.

```sh
curl -X POST localhost:8107/api/endpoints \
  -d '{"epEui":"02-00-00-00-00-00-00-01","nwkKey":"000102030405060708090A0B0C0D0E0F"}'
```

The EUI is written as eight pairs of upper case hex digits with hyphens, the key as 32
upper case hex digits. simple-sc answers with the new entry, without the key:

```json
{
  "epEui": "02-00-00-00-00-00-00-01",
  "class": "a",
  "state": "registered",
  ...
}
```

## 3. Simulate a base station

`sim-bs` connects like a real base station would. It takes the lab certificate authority
from `data`, gives itself a certificate, attaches the end-point and sends three uplinks:

```sh
./bin/sim-bs -ep 02-00-00-00-00-00-00-01:000102030405060708090A0B0C0D0E0F -uplinks 3
```

```
connected as 02-00-00-00-00-00-0B-01, session version 1.1.0
attached 02-00-00-00-00-00-00-01, short address DAF9, session key matches: true
uplink 02-00-00-00-00-00-00-01 packetCnt 1
uplink 02-00-00-00-00-00-00-01 packetCnt 2
uplink 02-00-00-00-00-00-00-01 packetCnt 3
```

"session key matches" means simple-sc derived the same session key as the end-point
itself, so the attachment worked. In the first terminal simple-sc logs the base station,
the attach and every uplink.

Try BSSCI 1.0 with `-version 1.0.0`, or two base stations at once with different `-eui`
values: an uplink both receive is stored once, with both receptions.

## 4. Read the uplinks

```sh
curl "localhost:8107/api/uplinks?limit=3"
```

```json
[
  {
    "epEui": "02-00-00-00-00-00-00-01",
    "packetCnt": 3,
    "userData": "03ABCD",
    "receptions": [ { "bsEui": "02-00-00-00-00-00-0B-01", "snr": 12, "rssi": -75 } ]
  },
  ...
]
```

And every message that went over the wire:

```sh
./bin/trace-view data/trace/trace.jsonl
```

```
14:32:15.893 bssci/02-00-00-00-00-00-0B-01  <- con     0  bidi=true bsClass=a bsEui=02-00-00-00-00-00-0B-01 ...
14:32:15.894 bssci/02-00-00-00-00-00-0B-01  -> conRsp  0  scEui=02-E0-C5-D4-B9-87-00-8C snResume=false ...
14:32:15.896 bssci/02-00-00-00-00-00-0B-01  <- att     1  attachCnt=1 epEui=02-00-00-00-00-00-00-01 ...
14:32:15.898 bssci/02-00-00-00-00-00-0B-01  -> attRsp  1  nwkSnKey=<redacted> shAddr=56057 ...
```

Keys never appear in the trace.

## Next

- Add the Application Center and receive the uplinks there: [Connecting an Application Center](application-center.md).
- Connect a real base station: [Connecting a base station](base-stations.md).
- Run simple-sc in the cloud: [Running on fly.io](fly-io.md).
- All settings: [Configuration](configuration.md).

For anything beyond a test, leave `SSCAC_BSSCI_UNKNOWN_BASE_STATIONS` at its default
`pending` and accept each base station yourself, as described in the base station guide.
