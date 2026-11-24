# Connecting a base station

A base station opens a TLS connection to simple-sc and keeps it open. Both sides show a
certificate: simple-sc its server certificate, the base station a client certificate
issued by the certificate authority of simple-sc. You need three things on the base
station:

1. the address and port of simple-sc (port **16107** unless you changed it),
2. the CA certificate of simple-sc, `data/ca/ca.pem`,
3. a client certificate and key for this base station, issued with `simple-sc ca issue`.

## 1. Make sure the server certificate fits the address

simple-sc issues its server certificate once, at the first start, for the host name of
the computer, `localhost` and `127.0.0.1`. A base station that connects to an IP address
or another name checks that name against the certificate. Add the names before the first
start:

```sh
SSCAC_BSSCI_CERT_HOSTS=192.168.1.10,sc.example.org ./bin/simple-sc
```

To change them later, stop simple-sc, delete `data/ca/sc.pem` and `data/ca/sc.key`, and
start it with the new `SSCAC_BSSCI_CERT_HOSTS`. The certificate authority stays the same,
so base stations do not need new certificates.

## 2. Issue a certificate for the base station

Use the EUI-64 of the base station, as printed on it or shown in its web interface:

```sh
./bin/simple-sc ca issue -name bs-1 -eui 70-B3-D5-00-00-00-00-01
```

```
certificate data/ca/issued/bs-1.pem
key         data/ca/issued/bs-1.key
CA          data/ca/ca.pem
fingerprint 1EF2E05FBCD8F30843FE82A12EB6266AA2B95E7943F56AA960404902D4D37537
```

## 3. Configure the base station

In the configuration of the base station, usually its web interface:

| Setting | Value |
|---|---|
| Service Center address | the address you added in step 1 |
| Service Center port | 16107 |
| CA certificate | `data/ca/ca.pem` |
| Client certificate | `data/ca/issued/bs-1.pem` |
| Client key | `data/ca/issued/bs-1.key` |

The names of these settings differ between products. Some base stations call the
Service Center "network server" or "backend".

Make sure the computer running simple-sc accepts incoming connections on port 16107. On
Windows, allow simple-sc in the firewall dialog that appears at the first start, or add a
rule for TCP port 16107.

## 4. Accept the base station

By default a new base station is connected as **pending**: simple-sc keeps the
connection, but gives it no end-points and ignores its uplinks until you accept it. This
way nobody can join your network just by having a certificate.

```sh
curl localhost:8107/api/basestations
```

```json
[ { "bsEui": "70-B3-D5-00-00-00-00-01", "state": "pending", "connected": true, ... } ]
```

```sh
curl -X POST localhost:8107/api/basestations/70-B3-D5-00-00-00-00-01/accept
```

From now on the base station is **enabled**. simple-sc sends it all attached end-points
and handles its attach requests and uplinks. The certificate is bound to this EUI: the
same EUI with another certificate is refused.

To lock a base station out:

```sh
curl -X POST localhost:8107/api/basestations/70-B3-D5-00-00-00-00-01/disable
```

## How simple-sc treats unknown base stations

Set with `SSCAC_BSSCI_UNKNOWN_BASE_STATIONS`:

| Value | Behaviour |
|---|---|
| `pending` (default) | Connected, but no end-point data until accepted |
| `accept` | Accepted at first contact. For a lab with only your own devices |
| `reject` | Refused. Only base stations you entered before may connect |

## BSSCI version

The base station asks for a version when it connects. simple-sc speaks 1.0 and 1.1 and
uses the one the base station asks for. On 1.0 there are no class B and C features. See
`SSCAC_BSSCI_POLICY` in the [configuration](configuration.md).

## Adding end-points

End-points have to be known to simple-sc before they can attach. Add each with its EUI
and network key, see the [REST interface](rest-api.md). An end-point that sends without
attaching first (unidirectional, class Z) is added with `"preAttach": true` and its short
address, and simple-sc passes it to all base stations at once.

## When it does not connect

| Symptom in the log of simple-sc | Cause |
|---|---|
| Nothing at all | The base station does not reach the address or port, or a firewall blocks it |
| `TLS handshake failed` | Wrong CA on the base station, client certificate not issued by this CA, or the address is not in the server certificate (step 1) |
| `connect refused: unknown base station` | `reject` mode and the base station is not entered |
| `connect refused: certificate does not match` | The EUI is bound to another certificate |
| `connect refused: EUI already connected` | A second connection with the same EUI, often an old one not yet closed. Wait a moment |
| `connect refused: version` | The base station asks for a BSSCI version simple-sc does not have a schema file for |

`trace-view data/trace/trace.jsonl` shows the exact messages.
