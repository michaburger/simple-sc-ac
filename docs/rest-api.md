# REST interface

simple-sc has a small REST interface for administration, on `127.0.0.1:8107` by default.
All answers are JSON. It has no authentication: keep it on your own computer or a private
network.

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

## Adding an end-point

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

## Uplinks

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
