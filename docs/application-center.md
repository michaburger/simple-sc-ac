# Connecting an Application Center

simple-ac is the Application Center. It keeps your end-points and their keys, registers
them at one or more Service Centers, and receives their uplinks. It opens the connection
to simple-sc itself (SCACI, port **17107**), so it can run anywhere, also behind a router.

The Service Center decides which Application Centers may connect. Each one gets a
certificate from the certificate authority of the Service Center, bound to its EUI-64.
Connecting takes three steps, one of them on the Service Center.

## 1. On the Application Center: create a key and a request

```sh
./bin/simple-ac sc request -name lab
```

```
Application Center EUI  02-8F-D3-66-21-EF-F2-B6
certificate request     data/sc/lab/client.csr
```

`lab` is the name you give this Service Center, used for a folder in `data/sc`. The
private key stays in `data/sc/lab/client.key` and never leaves this computer. Send the
EUI and `client.csr` to whoever runs the Service Center.

## 2. On the Service Center: add the Application Center

```sh
./bin/simple-sc ac add -eui 02-8F-D3-66-21-EF-F2-B6 -csr client.csr \
  -address sc.example.org:17107 -sc-name lab -name "my application" -out bundle.json
```

```
Application Center 02-8F-D3-66-21-EF-F2-B6 registered.
bundle bundle.json
```

| Option | Meaning |
|---|---|
| `-eui` | EUI-64 of the Application Center, as printed in step 1 |
| `-csr` | The request from step 1 |
| `-address` | How the Application Center reaches this Service Center, host and port |
| `-server-name` | Name in the server certificate, if it differs from the host in `-address` |
| `-sc-name` | Name of this Service Center on the Application Center, default `sc` |
| `-name` | Name of the Application Center, for your own overview |
| `-out` | Bundle file, default `data/ac/<EUI>.json` |

The address has to be in the server certificate of simple-sc, see
[base stations, step 1](base-stations.md#1-make-sure-the-server-certificate-fits-the-address).
Send `bundle.json` back to the Application Center. It contains no private key.

## 3. On the Application Center: import the bundle and start

```sh
./bin/simple-ac sc add bundle.json
./bin/simple-ac
```

```
... "msg":"connected to Service Center","sc":"lab","address":"sc.example.org:17107","version":"1.1.0"
... "msg":"admin REST listening","addr":"127.0.0.1:8108"
```

simple-ac keeps the connection up and reconnects by itself. It checks that the Service
Center has the EUI written in the bundle. `simple-ac sc list` shows all Service Centers.

## Onboarding over REST

In a container (Docker, fly.io, balena) there is no shell to run `simple-ac sc request`.
The same three steps work over the REST interfaces of both programs:

```sh
# 1. on the Application Center: key and request, the key stays there
curl -X POST localhost:8108/api/servicecenters/lab/request > request.json
# request.json: {"acEui": "02-...", "csr": "-----BEGIN CERTIFICATE REQUEST-----..."}

# 2. on the Service Center: register and sign, the answer is the bundle
curl -X POST localhost:8107/api/applicationcenters -d '{
  "acEui": "<acEui from request.json>", "csr": "<csr from request.json>",
  "name": "my application", "scName": "lab", "address": "sc.example.org:17107"
}' > bundle.json

# 3. on the Application Center: import, it connects at once
curl -X POST localhost:8108/api/servicecenters --data-binary @bundle.json
```

## Shortcut for a lab

When you run both on your own computer, skip the request: `simple-sc ac add` without
`-csr` creates the key as well and puts it into the bundle. Give the two programs
different data folders:

```sh
EUI=$(SSCAC_DATA_DIR=ac-data ./bin/simple-ac sc list | head -1 | awk '{print $NF}')
SSCAC_DATA_DIR=sc-data ./bin/simple-sc ac add -eui $EUI -address 127.0.0.1:17107 -sc-name lab -out bundle.json
SSCAC_DATA_DIR=ac-data ./bin/simple-ac sc add bundle.json
```

Then start both, each in its own terminal:

```sh
SSCAC_DATA_DIR=sc-data SSCAC_BSSCI_UNKNOWN_BASE_STATIONS=accept ./bin/simple-sc
SSCAC_DATA_DIR=ac-data ./bin/simple-ac
```

The Service Center has then seen the private key of the Application Center. That is fine
in a lab, not beyond. Keep the bundle file private.

## Add end-points

With an Application Center, add end-points there, not on the Service Center. simple-ac
registers each one at every connected Service Center, and again after every reconnect.

```sh
curl -X POST localhost:8108/api/endpoints \
  -d '{"epEui":"02-00-00-00-00-00-00-01","nwkKey":"000102030405060708090A0B0C0D0E0F"}'
```

```json
{
  "epEui": "02-00-00-00-00-00-00-01",
  "class": "a",
  "registrations": [ { "serviceCenter": "lab", "state": "registered" } ]
}
```

An end-point belongs to the Application Center that registered it first. Another
Application Center cannot register it, nor remove it.

## See what arrives

```sh
curl "localhost:8108/api/uplinks?limit=3"
curl localhost:8108/api/endpoints/02-00-00-00-00-00-00-01
```

The end-point shows its state (`attached`, `detached`) and the last base station that
heard it. All endpoints are in the [REST interface](rest-api.md#simple-ac).

## When the Application Center is offline

The Service Center keeps the uplinks of its end-points and delivers them when it is back:
up to 24 hours or 10 000 uplinks per Application Center, see `buffer_age` and
`buffer_max` in the [configuration](configuration.md). The usage counters of the Service
Center show what was delivered and what was dropped:

```sh
curl localhost:8107/api/usage
```

## Lock an Application Center out

On the Service Center:

```sh
curl -X POST localhost:8107/api/applicationcenters/02-8F-D3-66-21-EF-F2-B6/disable
```

Its connection is closed and refused from then on, its end-points stay registered. Enable
it again with `/enable`.

## When it does not connect

| Message in the log of simple-ac | Cause |
|---|---|
| `connection to Service Center failed ... connection refused` | Wrong address, simple-sc not running, or a firewall blocks port 17107 |
| `... certificate is valid for ..., not ...` | The address is not in the server certificate of simple-sc |
| `error 1: Application Center not registered` | Step 2 not done, or done on another Service Center |
| `error 1: certificate not bound to this Application Center EUI` | The bundle belongs to another EUI, or a newer bundle was issued |
| `error 1: Application Center disabled` | Locked out on the Service Center |
| `error 1: Application Center EUI already in use` | A second simple-ac with the same data folder or bundle is running |
