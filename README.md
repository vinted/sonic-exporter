# sonic-exporter

Prometheus exporter for [SONiC](https://github.com/sonic-net/SONiC) NOS.

Currently supported collectors:
- [HW collector](internal/collector/hw_collector.go): collects metrics about PSU and Fan operation
- [Interface collector](internal/collector/interface_collector.go): collect metrics about interface operation and performance.
- [CRM collector](internal/collector/hw_collector.go): collects Critial Resource Monitoring metrics.
- [Queue collector](internal/collector/queue_collector.go): collects metrics about queues.

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

Environment variables:

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
