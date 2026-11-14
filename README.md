# sonic-exporter

Prometheus exporter for [SONiC](https://github.com/sonic-net/SONiC) NOS.

Currently supported collectors. The first four are enabled by default; the lossless-fabric collectors below them are opt-in with `--collector.<name>`:

| Collector | Flag | Description |
| --- | --- | --- |
| [Interface](internal/collector/interface_collector.go) | `--collector.interface` | Metrics about interface operation and performance. |
| [HW](internal/collector/hw_collector.go) | `--collector.hw` | Metrics about PSU and Fan operation. |
| [CRM](internal/collector/crm_collector.go) | `--collector.crm` | Critical Resource Monitoring metrics. |
| [Queue](internal/collector/queue_collector.go) | `--collector.queue` | Metrics about queues. |
| [PFC](internal/collector/pfc_collector.go) | `--collector.pfc` (off) | PFC pause frames received and transmitted per port and priority. |
| [PG](internal/collector/pg_collector.go) | `--collector.pg` (off) | Ingress priority group shared/headroom watermarks, PG drops when the switch has the counter. `--collector.pg.include-pgs=3,4` (default), a range or `all`. |
| [Buffer pool](internal/collector/buffer_collector.go) | `--collector.buffer_pool` (off) | Buffer pool watermarks. |
| [Queue info](internal/collector/buffer_collector.go) | `--collector.queue_info` (off) | Queue type (unicast, multicast) per queue. |
| [QoS config](internal/collector/qos_config_collector.go) | `--collector.qos_config` (off) | Configured lossless setup from CONFIG_DB: PFC priorities, QoS maps, buffer profiles per PG and queue, WRED/ECN, PFC watchdog. |
| [ECN/PFCWD](internal/collector/ecn_pfcwd_collector.go) | `--collector.ecn_pfcwd` (off) | ECN marks, WRED drops, PFC watchdog counters; series appear only if the switch exports the fields. |
| [LLDP](internal/collector/lldp_collector.go) | `--collector.lldp` (off) | LLDP neighbour of each port. |
| [Queue limit](internal/collector/queue_limit_collector.go) | `--collector.queue_limit` (off) | Egress queue buffer limit (BUFFER_QUEUE profile, or `--collector.queue_limit.default-bytes` for queues without one). |
| [QoS map](internal/collector/qos_map_collector.go) | `--collector.qos_map` (off) | DSCP/PCP to traffic class, class to queue and PG maps, and the map bound to each port. |

Every collector also exports `sonic_<collector>_last_refresh_timestamp_seconds`. The pg collector takes `--collector.pg.include-pgs` and `--collector.pg.types`. Design documents: [docs/design/README.md](docs/design/README.md).

The exporter also exposes `sonic_exporter_build_info{version,revision}` and `sonic_exporter_errors_total{collector,reason}` (values that could not be parsed or redis calls that failed; a bad value skips only its own series).

# Usage

1. Run binary 
```bash
$ ./sonic-exporter
```

2. You can verify that exporter is running by cURLing the `/metrics` endpoint. 
```bash
$ curl localhost:9101/metrics
```

# Configuration

## Collectors

The four original collectors are enabled by default. To disable one, pass its flag with the
`--no-` prefix; to enable an opt-in collector, pass `--collector.<name>`:

```bash
$ ./sonic-exporter --no-collector.queue --no-collector.crm
```

Run `./sonic-exporter --help` for the full list of flags.

## Environment variables

- `REDIS_ADDRESS` - redis connection string, if using unix socket set `REDIS_NETWORK` to `unix`. Default: `localhost:6379`.
- `REDIS_PASSWORD` - password used when connecting to redis.
- `REDIS_NETWORK` - redis network type, either tcp or unix. Default: `tcp`.

# Development

1. Development environment is based on docker-compose. To start it run:
```bash
$ docker-compose up --build -d
```

2. To verify that development environment is ready try cURLing the `/metrics` endpoint, you should see exported metrics.:
```bash
$ curl localhost:9101/metrics
```

3. After making code changes rebuild docker container:
```bash
$ docker-compose down
$ docker-compose up --build -d
```

In case you need to add additional keys to redis database don't forget to run `SAVE` in redis after doing so:
```bash
$ redis-cli
$ 127.0.0.1:6379> SAVE
```

## Test

Currently, tests are using mockredis database which is populated from [fixture files](fixtures/test/).
To run tests manually simply execute:
```bash
$ go test -v ./... 
```
