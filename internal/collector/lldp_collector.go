package collector

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

// NewLldpCollector exports the LLDP neighbour of every port (APPL_DB LLDP_ENTRY_TABLE), so that switch
// port series can be joined to the host that is cabled to the port.
func NewLldpCollector(logger log.Logger) *cachedCollector {
	neighbor := prometheus.NewDesc("sonic_lldp_neighbor_info", "LLDP neighbour of a port, value is always 1",
		[]string{"device", "remote_system_name", "remote_port_id", "remote_chassis_id"}, nil)

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		keys, err := rc.ScanKeys(ctx, "APPL_DB", "LLDP_ENTRY_TABLE:*")
		if err != nil {
			return nil, fmt.Errorf("redis scan failed: %w", err)
		}

		hashes, err := rc.PipelineHGetAll(ctx, "APPL_DB", keys)
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		var metrics []prometheus.Metric

		for key, hash := range hashes {
			device := strings.TrimPrefix(key, "LLDP_ENTRY_TABLE:")

			metrics = append(metrics, prometheus.MustNewConstMetric(neighbor, prometheus.GaugeValue, 1,
				device, hash["lldp_rem_sys_name"], hash["lldp_rem_port_id"], hash["lldp_rem_chassis_id"]))
		}

		return metrics, nil
	}

	return newCachedCollector("lldp", logger, scrape, neighbor)
}
