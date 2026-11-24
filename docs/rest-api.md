# REST interface

Both programs have a small REST interface for administration: simple-sc on
`127.0.0.1:8107`, simple-ac on `127.0.0.1:8108`. All answers are JSON. They have no
authentication: keep them on your own computer or a private network.

- [simple-sc](#simple-sc)
- [simple-ac](#simple-ac)

## simple-sc

| Method and path | What it does |
|---|---|
| `GET /api/status` | Version, EUI, connected base stations, number of end-points |
| `GET /api/basestations` | All base stations with state and last status |
| `POST /api/basestations/{eui}/accept` | Accept a pending base station |
| `POST /api/basestations/{eui}/disable` | Lock a base station out and close its connection |
| `GET /api/endpoints` | All end-points, without keys |
| `POST /api/endpoints` | Add or replace an end-point |
| `GET /api/endpoints/{eui}` | One end-point |
| `DELETE /api/endpoints/{eui}` | Remove an end-point and detach it at all base stations |
| `GET /api/uplinks?ep={eui}&limit={n}` | The newest uplinks, of all end-points or of one, default 50 |
| `GET /api/applicationcenters` | Registered Application Centers, connected or not |
| `POST /api/applicationcenters` | Register an Application Center and get its bundle, see [onboarding over REST](application-center.md#onboarding-over-rest) |
| `POST /api/applicationcenters/{eui}/disable` | Lock an Application Center out and close its connection |
| `POST /api/applicationcenters/{eui}/enable` | Let it connect again |
| `GET /api/usage?days={n}` | Usage counters per Application Center and day, default 7 days |

With an Application Center, add end-points there. The end-point endpoints of simple-sc
are for running it alone.

### Adding an end-point

```sh
curl -X POST localhost:8107/api/endpoints -d '{
  "epEui": "02-00-00-00-00-00-00-01",
  "nwkKey": "000102030405060708090A0B0C0D0E0F",
  "class": "a"
}'
```

| Field | Required | Meaning |
|---|---|---|
| `epEui` | yes | EUI-64 of the end-point, `70-B3-D5-...` form, upper case |
| `nwkKey` | yes | Network key, 32 upper case hex digits |
| `class` | no | `z` (unidirectional), `a` (default), `b` or `c` |
| `preAttach` | no | `true` for an end-point that does not attach over the air |
| `shAddr` | with `preAttach` | Short address, 4 upper case hex digits, e.g. `1A2B` |
| `attachCnt`, `packetCnt` | no | Counters to start from, for a device that was used before |

The answer is the stored entry. Keys are never returned.

### Uplinks

```json
{
  "id": 3,
  "epEui": "02-00-00-00-00-00-00-01",
  "packetCnt": 3,
  "rxTime": 1795527136116065700,
  "format": 0,
  "userData": "03ABCD",
  "receptions": [
    { "bsEui": "02-00-00-00-00-00-0B-01", "rxTime": 1795527136116065700, "snr": 12, "rssi": -75 }
  ]
}
```

`rxTime` is the time of reception in nanoseconds since 1970 (UTC). `userData` is the
payload as hex. `receptions` has one entry per base station that received it.

### Usage counters

```json
[
  {
    "acEui": "02-8F-D3-66-21-EF-F2-B6",
    "day": "2026-11-24",
    "counters": { "registrations": 1, "uplinks": 3, "uplinks_delivered": 3 },
    "activeEndPoints": 1
  }
]
```

| Counter | Meaning |
|---|---|
| `registrations` | End-point registrations received |
| `uplinks` | Uplinks of the end-points of this Application Center |
| `uplinks_delivered` | Uplinks delivered to it |
| `uplinks_buffered` | Uplinks that arrived while it was offline |
| `uplinks_dropped` | Buffered uplinks that were older or more than the limit |
| `activeEndPoints` | End-points that sent at least once that day |

## simple-ac

| Method and path | What it does |
|---|---|
| `GET /api/status` | Version, EUI, Service Centers and their connection |
| `GET /api/servicecenters` | The Service Centers from `data/sc` |
| `POST /api/servicecenters/{name}/request` | Create key and certificate request for a Service Center |
| `POST /api/servicecenters` | Import a bundle and connect |
| `GET /api/servicecenters/{name}/status` | Ask a Service Center for its status and its base stations |
| `GET /api/endpoints` | All end-points with registrations and state, without keys |
| `POST /api/endpoints` | Add or replace an end-point and register it at every connected Service Center |
| `GET /api/endpoints/{eui}` | One end-point |
| `DELETE /api/endpoints/{eui}` | Deregister an end-point everywhere and remove it |
| `GET /api/uplinks?ep={eui}&limit={n}` | The newest uplinks, default 50 |

The body of `POST /api/endpoints` has the same fields as on simple-sc, plus an optional
`name`. An end-point shows where it is registered:

```json
{
  "epEui": "02-00-00-00-00-00-00-01",
  "class": "a",
  "state": { "status": "attached", "lastServiceCenter": "lab", "lastBaseStation": "02-00-00-00-00-00-0B-01" },
  "registrations": [ { "serviceCenter": "lab", "state": "registered" } ]
}
```

`state` of a registration is `registered`, `pending` (the Service Center is not connected,
it is registered after the next connect) or `failed` with an `error`.
