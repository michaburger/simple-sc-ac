# simple-sc-ac

A small, open source mioty Service Center and Application Center. Run your own mioty
network backend on a laptop, a Raspberry Pi or a small cloud machine, connect a base
station, and get decoded sensor data on MQTT.

It is meant for developers, labs and anyone who wants to try mioty end to end without a
commercial backend, or who needs a simple, readable counterpart for testing a base station
or an application.

```
End-point ~~radio~~> Base station ──BSSCI──> simple-sc ──SCACI──> simple-ac ──MQTT──> Your application
```

- **simple-sc**, the Service Center (SC): accepts base stations, attaches end-points,
  receives their messages.
- **simple-ac**, the Application Center (AC): knows your end-points and their keys,
  decodes the payloads and publishes them on MQTT.

The two programs are independent. You can run both on one machine, or one SC for several
ACs, or one AC connected to several SCs.

## Status

**Early development.** What works today:

- `simple-sc` accepts base stations over BSSCI 1.0 and 1.1 (TLS on port 16107), attaches
  end-points over the air, propagates them to the other base stations, and stores
  deduplicated uplinks.
- End-points are entered through a local REST interface on `127.0.0.1:8107` for now.
- `sim-bs` simulates a base station with end-points, `trace-view` shows every message.

Not there yet: the Application Center `simple-ac`, MQTT output, downlinks. Watch or star
the repository to follow.

simple-sc reads the message definitions of the interfaces from schema files in its data
directory (`data/schemas`). They are derived from the mioty specifications and are not
part of this repository, see "mioty interfaces" below.

## Planned ways to run it

One executable per program, no installation, no database server. Configuration with a
small file or with environment variables, and it starts with sensible defaults.

| Where | How |
|---|---|
| Laptop (Linux, Windows, macOS) | Download the executables, or `docker compose up` |
| Raspberry Pi CM4 or CM5 | balena, with a deploy button |
| Cloud | fly.io |

## mioty interfaces

| Interface | Between | Versions |
|---|---|---|
| BSSCI | Base station and SC | 1.0 and 1.1, chosen per base station |
| SCACI | SC and AC | 1.1, fallback 1.0 |
| ACINT | AC and application (MQTT) | 0.0.1 |
| End-Point Passport | Device data import into the AC | 1.0.0 |
| Application Layer (blueprints) | Payload decoding | 1.1.0 |

The interface specifications themselves are published by the
[mioty Alliance](https://mioty-alliance.com), always in their latest version. This
repository does not copy them.

Some of these interface versions are drafts of the mioty Alliance and will still change.
simple-sc-ac follows them, so expect breaking changes until they are final. Nothing in this
repository is a normative statement about mioty.

## What it is not

- Not a production network server: no high availability, no user management, no billing.
- Not a replacement for commercial mioty backends. It is simple on purpose.

## Licence

Apache License 2.0, see [LICENSE](LICENSE).

## Questions and bugs

Open an issue on GitHub. Contributions are welcome once the first version is out, a short
contributing guide will follow.
