# simple-sc-ac

An open source mioty Service Center and Application Center, small enough to read and run
yourself. Connect a mioty base station to your own laptop, a Raspberry Pi or a small cloud
machine, and see what your end-points send.

```
End-point ~~radio~~> Base station ──BSSCI──> simple-sc ──SCACI──> simple-ac ──MQTT──> Your application
```

- **simple-sc** is the Service Center. Base stations connect to it, it attaches the
  end-points and collects their uplinks.
- **simple-ac** is the Application Center. It will hold your end-points and their keys,
  decode the payloads and publish them on MQTT.

## What works today

- simple-sc accepts base stations over BSSCI 1.0 and 1.1, with TLS and client
  certificates.
- End-points attach over the air or are pre-attached. Uplinks received by several base
  stations are merged into one.
- A local REST interface to add end-points, accept base stations and read uplinks.
- `sim-bs`, a simulated base station, so you can try everything without hardware.
- `trace-view`, to read every message that went over the wire.

simple-ac, the MQTT output and downlinks are not there yet.

## What it is not

- Not a production network server: no high availability, no user management, no billing.
- Not a replacement for commercial mioty backends. It is simple on purpose.

## Quick start

You need [Go](https://go.dev/dl/) 1.26 or newer.

```sh
git clone https://github.com/michaburger/simple-sc-ac.git
cd simple-sc-ac
go build -o bin/ ./cmd/...
./bin/simple-sc
```

The first start creates a `data` folder with a lab certificate authority, the server
certificate and an empty `data/schemas` folder. Put the interface schema files there and
start again. The [getting started guide](docs/getting-started.md) takes you from here to
a first uplink in a few minutes, with a simulated base station.

## Documentation

| Guide | What you learn |
|---|---|
| [Installation](docs/installation.md) | Build from source, run with Docker, the data folder |
| [Getting started](docs/getting-started.md) | First run, add an end-point, simulate a base station, read the uplinks |
| [Connecting a base station](docs/base-stations.md) | Certificates, address and port, accepting a new base station |
| [Configuration](docs/configuration.md) | Every setting, as file entry and as environment variable |
| [REST interface](docs/rest-api.md) | Endpoints of the local admin interface |
| [Running on fly.io](docs/fly-io.md) | Deploy simple-sc to the cloud, and keep the cost low |
| [Tools](docs/tools.md) | `sim-bs` and `trace-view` |

## mioty interfaces

| Interface | Between | Versions |
|---|---|---|
| BSSCI | Base station and Service Center | 1.0 and 1.1, chosen per base station |
| SCACI | Service Center and Application Center | 1.1 and 1.0 (simple-ac, in progress) |
| ACINT | Application Center and application, over MQTT | 0.0.1 (in progress) |

simple-sc reads the message definitions of each interface version from schema files in
`data/schemas`. They are derived from the mioty specifications, which are published by
the [mioty Alliance](https://mioty-alliance.com), and are not part of this repository.
Some interface versions are drafts and will still change, so expect breaking changes.
Nothing here is a normative statement about mioty.

## Licence

Apache License 2.0, see [LICENSE](LICENSE).

## Questions and bugs

Open an issue on GitHub.
