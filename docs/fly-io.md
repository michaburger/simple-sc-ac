# Running on fly.io

[fly.io](https://fly.io) runs simple-sc on a small machine in a data centre, so base
stations anywhere can reach it. You need a fly.io account and
[flyctl](https://fly.io/docs/flyctl/install/).

Base stations connect with TLS directly to simple-sc, not through HTTP. This needs a
dedicated IPv4 address on fly.io, which costs money. See [cost](#cost) before you start.

## Deploy

Choose a name for your app, it has to be unique on fly.io. The examples use
`my-simple-sc`. Run all commands in the repository folder.

```sh
fly apps create my-simple-sc
fly volumes create sc_data --size 1 --region fra -a my-simple-sc
fly ips allocate-v4 -a my-simple-sc
fly deploy --config deploy/fly/fly.sc.toml -a my-simple-sc --remote-only
```

`fra` is Frankfurt. Pick the [region](https://fly.io/docs/reference/regions/) closest to
your base stations, for the volume and in `primary_region` of
`deploy/fly/fly.sc.toml`.

Check that it runs:

```sh
fly status -a my-simple-sc
fly logs -a my-simple-sc
```

## Connect base stations

Base stations connect to the dedicated IPv4 address (`fly ips list -a my-simple-sc`) on
port 16107, with the server name `my-simple-sc.fly.dev`. The server certificate contains
that name.

The certificate authority is on the volume. Fetch the CA certificate for your base
stations:

```sh
fly ssh sftp get /data/ca/ca.pem ca.pem -a my-simple-sc
```

To issue base station certificates on your own computer, also fetch the CA key into a
folder `fly-ca/ca` and use a local `simple-sc` with that folder as data folder:

```sh
mkdir -p fly-ca/ca
fly ssh sftp get /data/ca/ca.pem fly-ca/ca/ca.pem -a my-simple-sc
fly ssh sftp get /data/ca/ca.key fly-ca/ca/ca.key -a my-simple-sc
SSCAC_DATA_DIR=fly-ca ./bin/simple-sc ca issue -name bs-1 -eui 70-B3-D5-00-00-00-00-01
```

Keep `fly-ca` private, it holds the key of your network's certificate authority.

## Administration

The REST interface is only on the private network of your fly.io organisation. Open a
tunnel to it:

```sh
fly proxy 18107:8107 -a my-simple-sc
```

and use `localhost:18107` instead of `localhost:8107` in the
[REST interface](rest-api.md) commands. Every app in the same fly.io organisation can
reach this interface, so keep simple-sc in an organisation of its own.

## Test from your computer

With the CA fetched into `fly-ca`, `sim-bs` connects over the internet:

```sh
./bin/sim-bs -sc <IPv4 address>:16107 -server-name my-simple-sc.fly.dev -data fly-ca \
  -ep 02-00-00-00-00-00-00-01:000102030405060708090A0B0C0D0E0F -uplinks 3
```

Accept the simulated base station and add the end-point through the tunnel first, as in
[getting started](getting-started.md).

## Updates

```sh
git pull
fly deploy --config deploy/fly/fly.sc.toml -a my-simple-sc --remote-only
```

Everything on the volume stays: EUI, certificate authority, base stations, end-points.

## Cost

From the fly.io pricing page at the time of writing, per month:

| Item | Running | Stopped |
|---|---|---|
| Machine shared-cpu-1x, 256 MB | about $2.20 to $2.60, depending on the region | almost nothing |
| Volume 1 GB | $0.15 | $0.15 |
| Dedicated IPv4 | $2.00 | $2.00 |
| Outbound traffic | $0.02 per GB, a few cents for a test network | |

Stop the machine when you do not need it. Nothing is lost, but no base station can
connect while it is stopped:

```sh
fly machine list -a my-simple-sc          # shows the machine ID
fly machine stop <machine ID> -a my-simple-sc
fly machine start <machine ID> -a my-simple-sc
```

Releasing the IPv4 address saves the rest, but you get a new address next time and have
to reconfigure your base stations.
