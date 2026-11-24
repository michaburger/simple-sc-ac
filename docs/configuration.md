# Configuration

simple-sc works without a configuration file. Change a setting with an environment
variable, or put it into a TOML file and start with `-config sc.toml` (or set
`SSCAC_CONFIG`). Environment variables win over the file. An unknown key or variable is an
error, so a typo does not go unnoticed.

The environment variable of a setting is `SSCAC_` plus the section and the key in upper
case: `listen` in `[bssci]` is `SSCAC_BSSCI_LISTEN`.

## Example file

```toml
data_dir = "/var/lib/simple-sc"
log_level = "info"

[bssci]
listen = ":16107"
unknown_base_stations = "pending"
cert_hosts = "192.168.1.10,sc.example.org"

[admin]
listen = "127.0.0.1:8107"
```

## General

| Key | Variable | Default | Meaning |
|---|---|---|---|
| `data_dir` | `SSCAC_DATA_DIR` | `data`, `/data` in a container | Where everything is kept |
| `schema_dir` | `SSCAC_SCHEMA_DIR` | `<data_dir>/schemas` | Interface schema files |
| `eui` | `SSCAC_EUI` | derived, see [installation](installation.md#the-eui-of-simple-sc) | EUI-64 of this Service Center, e.g. `70-B3-D5-00-00-00-00-AA` |
| `log_level` | `SSCAC_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `trace_file` | `SSCAC_TRACE_FILE` | `<data_dir>/trace/trace.jsonl` | Message trace, `-` switches it off |

## `[bssci]` base stations

| Key | Variable | Default | Meaning |
|---|---|---|---|
| `listen` | `SSCAC_BSSCI_LISTEN` | `:16107` | Address and port for base stations |
| `unknown_base_stations` | `SSCAC_BSSCI_UNKNOWN_BASE_STATIONS` | `pending` | `pending`, `accept` or `reject`, see [base stations](base-stations.md) |
| `cert_hosts` | `SSCAC_BSSCI_CERT_HOSTS` | | Extra names and addresses for the server certificate, comma separated. Used at the first start only |
| `cert_file`, `key_file` | `SSCAC_BSSCI_CERT_FILE`, `SSCAC_BSSCI_KEY_FILE` | the lab certificate | Your own server certificate and key (PEM) |
| `ca_file` | `SSCAC_BSSCI_CA_FILE` | the lab CA | CA that issues the base station certificates (PEM) |
| `policy` | `SSCAC_BSSCI_POLICY` | `follow` | `follow`: use the version the base station asks for. `offer-highest`: always offer the highest version first, a test option |
| `mode` | `SSCAC_BSSCI_MODE` | `lenient` | `lenient` accepts small deviations from the specification and logs them, `strict` refuses them |
| `response_timeout` | `SSCAC_BSSCI_RESPONSE_TIMEOUT` | `5s` | How long to wait for an answer |
| `idle_ping` | `SSCAC_BSSCI_IDLE_PING` | `10s` | Ping after this much silence, `0s` off |
| `status_interval` | `SSCAC_BSSCI_STATUS_INTERVAL` | `60s` | How often to ask base stations for their status, `0s` off |
| `dedup_window` | `SSCAC_BSSCI_DEDUP_WINDOW` | `500ms` | Receptions of the same uplink within this time are merged |

Durations are written like `500ms`, `10s`, `5m`.

## `[admin]` REST interface

| Key | Variable | Default | Meaning |
|---|---|---|---|
| `listen` | `SSCAC_ADMIN_LISTEN` | `127.0.0.1:8107` | Address of the [REST interface](rest-api.md), `-` switches it off |

The REST interface has no authentication. Keep it on `127.0.0.1` or on a private
network.

## Using your own certificates

If you have your own PKI, set `cert_file` and `key_file` to the server certificate of
simple-sc and `ca_file` to the CA that issues your base station certificates. simple-sc
then still binds each base station EUI to the certificate it first connected with.
