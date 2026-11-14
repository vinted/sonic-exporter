# sonic-exporter design documents

One document per feature of the lossless-fabric work. Each has 5W1H, a deployment diagram, a data-flow diagram, configuration, results with their source, caveats and rollback.

| Feature | Flag | Document |
|---|---|---|
| PFC per priority | `--collector.pfc` | [pfc.md](pfc.md) |
| Priority group watermarks (all PGs, selectable types) | `--collector.pg` | [pg.md](pg.md) |
| Buffer pool watermarks, queue types | `--collector.buffer_pool`, `--collector.queue_info` | [buffer-pool-and-queue-info.md](buffer-pool-and-queue-info.md) |
| Lossless configuration compliance | `--collector.qos_config` | [qos-config.md](qos-config.md) |
| ECN, WRED and PFC watchdog counters | `--collector.ecn_pfcwd` | [ecn-pfcwd.md](ecn-pfcwd.md) |
| LLDP neighbours | `--collector.lldp` | [lldp.md](lldp.md) |
| Egress queue limit | `--collector.queue_limit` | [queue-limit.md](queue-limit.md) |
| Classification maps | `--collector.qos_map` | [qos-map.md](qos-map.md) |
| Refresh timestamps | always on | [refresh-timestamp.md](refresh-timestamp.md) |

Labels used in the documents: **M** measured, **V** verified state, **T** expected/theory, **H** hypothesis.
Dashboards: [../../dashboards/README.md](../../dashboards/README.md).
