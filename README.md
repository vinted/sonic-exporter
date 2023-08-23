# sonic-exporter

Environment variables:

- `REDIS_ADDRESS` - redis connection string, if using unix socket set `REDIS_NETWORK` to `unix`. Default: `localhost:6379`.
- `REDIS_PASSWORD` - password used when connecting to redis.
- `REDIS_NETWORK` - redis network type, either tcp or unix. Default: `tcp`.
