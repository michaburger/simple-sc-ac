# Installation

simple-sc is one program without dependencies: no database server, no runtime, no
installer. Choose the way that suits you.

- [From source](#from-source)
- [With Docker](#with-docker)
- [The data folder](#the-data-folder)

## From source

You need [Go](https://go.dev/dl/) 1.26 or newer and git.

```sh
git clone https://github.com/michaburger/simple-sc-ac.git
cd simple-sc-ac
go build -o bin/ ./cmd/...
```

This builds four programs into `bin/`: `simple-sc`, `simple-ac`, `sim-bs` and
`trace-view` (with `.exe` on Windows). Check it:

```sh
./bin/simple-sc -version
```

To build for another platform, set `GOOS` and `GOARCH`. The programs are pure Go, so this
works from any computer, for example for a Raspberry Pi:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/linux-arm64/ ./cmd/...
```

## With Docker

Build the image:

```sh
docker build --build-arg PROGRAM=simple-sc -f deploy/docker/Dockerfile -t simple-sc .
```

Run it with a named volume for the data and the BSSCI port published:

```sh
docker run -d --name simple-sc \
  -p 16107:16107 \
  -p 127.0.0.1:8107:8107 -e SSCAC_ADMIN_LISTEN=0.0.0.0:8107 \
  -v simple-sc-data:/data \
  simple-sc
```

The admin REST interface has no authentication. Inside the container it has to listen on
all interfaces (`SSCAC_ADMIN_LISTEN`), and `-p 127.0.0.1:8107:8107` makes sure it is only
reachable from your own computer.

Check the log:

```sh
docker logs simple-sc
```

The image is small and has no shell. To look at the data, use `docker cp` to copy files
out, for example the lab CA certificate for a base station:

```sh
docker cp simple-sc:/data/ca/ca.pem .
```

The image runs on `linux/amd64` and `linux/arm64`. To build both at once:

```sh
docker buildx build --platform linux/amd64,linux/arm64 --build-arg PROGRAM=simple-sc -f deploy/docker/Dockerfile .
```

## The data folder

Everything simple-sc keeps is in one folder: `data` next to where you start it, or
`/data` in a container. Set another one with `SSCAC_DATA_DIR`.

| Path | Content |
|---|---|
| `identity/sc.eui` | The EUI-64 of this Service Center, created at the first start |
| `ca/ca.pem`, `ca/ca.key` | The lab certificate authority. Keep `ca.key` private |
| `ca/sc.pem`, `ca/sc.key` | The server certificate base stations see |
| `ca/issued/` | Certificates you issued with `simple-sc ca issue` |
| `schemas/` | Empty. A [schema file](../schemas/README.md) here replaces the built-in one of the same version |
| `sc.sqlite` | Base stations, end-points, uplinks |
| `trace/trace.jsonl` | Every BSSCI message, one per line, keys removed |

Back up the folder to keep your network: copy it while simple-sc is stopped. Delete it to
start from scratch, with a new EUI and a new certificate authority.

### The EUI of simple-sc

At the first start simple-sc gives itself an EUI-64 and keeps it in `identity/sc.eui`.
It is derived from a hardware address of the computer and starts with `02`, which marks
it as locally administered, so it can never collide with an EUI assigned by the IEEE.
If your organisation has its own EUI range, set the EUI yourself with `SSCAC_EUI`.
