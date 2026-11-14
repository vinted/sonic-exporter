package collector

import (
	"context"
	"strings"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

// NewQosMapCollector exports the content of the traffic classification maps and which map is bound to each port, so that a
// rule can tell which DSCP / PCP value lands in which traffic class (for example "default DSCP 0 shares the lossless class").
func NewQosMapCollector(logger log.Logger) *cachedCollector {
	dscp := prometheus.NewDesc("sonic_dscp_to_tc", "DSCP value mapped to a traffic class by a DSCP_TO_TC_MAP, value is always 1", []string{"name", "dscp", "tc"}, nil)
	dot1p := prometheus.NewDesc("sonic_dot1p_to_tc", "PCP value mapped to a traffic class by a DOT1P_TO_TC_MAP, value is always 1", []string{"name", "dot1p", "tc"}, nil)
	tcQueue := prometheus.NewDesc("sonic_tc_to_queue", "Traffic class mapped to an egress queue (TC_TO_QUEUE_MAP), value is always 1", []string{"name", "tc", "queue"}, nil)
	tcPg := prometheus.NewDesc("sonic_tc_to_pg", "Traffic class mapped to an ingress priority group (TC_TO_PRIORITY_GROUP_MAP), value is always 1", []string{"name", "tc", "pg"}, nil)
	binding := prometheus.NewDesc("sonic_port_qos_map_info", "QoS map bound to a port (PORT_QOS_MAP), value is always 1", []string{"device", "type", "name"}, nil)

	tables := map[string]struct {
		desc   *prometheus.Desc
		labels int
	}{
		"DSCP_TO_TC_MAP": {dscp, 2}, "DOT1P_TO_TC_MAP": {dot1p, 2}, "TC_TO_QUEUE_MAP": {tcQueue, 2}, "TC_TO_PRIORITY_GROUP_MAP": {tcPg, 2},
	}

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		hashes, err := configKeys(ctx, rc, "DSCP_TO_TC_MAP|*", "DOT1P_TO_TC_MAP|*", "TC_TO_QUEUE_MAP|*", "TC_TO_PRIORITY_GROUP_MAP|*", "PORT_QOS_MAP|*")
		if err != nil {
			return nil, err
		}

		var metrics []prometheus.Metric

		for key, hash := range hashes {
			table, name, _ := strings.Cut(key, "|")

			if table == "PORT_QOS_MAP" {
				for _, field := range qosMaps {
					if hash[field] != "" {
						metrics = append(metrics, prometheus.MustNewConstMetric(binding, prometheus.GaugeValue, 1, name, field, stripBrackets(hash[field])))
					}
				}

				continue
			}

			spec, ok := tables[table]
			if !ok {
				continue
			}

			for from, to := range hash {
				metrics = append(metrics, prometheus.MustNewConstMetric(spec.desc, prometheus.GaugeValue, 1, name, from, to))
			}
		}

		return metrics, nil
	}

	return newCachedCollector("qos_map", logger, scrape, dscp, dot1p, tcQueue, tcPg, binding)
}
