# SONiC Exporter Dashboards

This directory contains Grafana dashboards for the sonic-exporter These dashboards provide a comprehensive view of your SONiC Network Operating System (NOS) health and performance.

## 📊 Dashboard Overview

The included dashboard is designed to visualize metrics from all core collectors supported by the exporter:

* **Hardware (HW):** PSU status, Fan speeds, and thermal operation.
* **Interfaces:** throughput (bits/sec), packet rates, error counters.
* **Critical Resource Monitoring (CRM):** ACL, FDB, and Route table utilization.
* **Queues:** egress/ingress queue lenghts and packet drops.

## Lossless-fabric rows

Rows at the bottom of the dashboard (panel ids 200 and above) cover the opt-in collectors. A panel shows an explicit "No data" message when its collector is not enabled or the switch does not export the counter, so an empty panel is never silent.

| Row | Collectors | What it answers |
| --- | --- | --- |
| PFC per priority | `pfc` | Who pauses this switch, and does the switch ever pause anyone (TX must be non-zero when a lossless queue overloads) |
| Priority groups, buffer pools, queue limit | `pg`, `buffer_pool`, `queue_limit` | Where traffic is accounted on ingress (use `--collector.pg.include-pgs=0-7 --collector.pg.types=periodic`), whether PFC headroom is used, how close each egress queue is to its limit |
| Configuration compliance and classification | `qos_config`, `qos_map` | Ports with PFC but no egress lossless profile, unbound QoS maps, which DSCP values land in the lossless class, which map each port uses |
| ECN, PFC watchdog, neighbours | `ecn_pfcwd`, `lldp` | ECN/WRED/watchdog activity (only when the switch counts it, see the availability panel), LLDP neighbours |
| Exporter health | all | Collector success, age of the data (`*_last_refresh_timestamp_seconds`), skipped values (`sonic_exporter_errors_total`), scrape duration |

The rows are generated: `python3 dashboards/gen_lossless_rows.py dashboards/sonic-exporter.json` (idempotent). Design documents: [../docs/design/README.md](../docs/design/README.md).

## How to Import

1. Download the `sonic-exporter.json` file from this repository.
2. Import file in grafana
3. Select your Prometheus data source in the configuration screen.
