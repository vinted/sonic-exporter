package collector

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

// portCounters reads COUNTERS:<port oid> for every port in COUNTERS_PORT_NAME_MAP in one pipeline.
func portCounters(ctx context.Context, rc redis.Client) (map[string]map[string]string, error) {
	ports, err := rc.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_PORT_NAME_MAP")
	if err != nil {
		return nil, fmt.Errorf("redis read failed: %w", err)
	}

	keys := make([]string, 0, len(ports))
	byKey := make(map[string]string, len(ports))

	for port, oid := range ports {
		keys = append(keys, "COUNTERS:"+oid)
		byKey["COUNTERS:"+oid] = port
	}

	hashes, err := rc.PipelineHGetAll(ctx, "COUNTERS_DB", keys)
	if err != nil {
		return nil, fmt.Errorf("redis read failed: %w", err)
	}

	result := make(map[string]map[string]string, len(ports))
	for key, hash := range hashes {
		result[byKey[key]] = hash
	}

	return result, nil
}

// NewPfcCollector exports the per priority PFC pause frame counters of every port. They are in the same
// COUNTERS:<port oid> hash the interface collector reads, but were never exported.
func NewPfcCollector(logger log.Logger) *cachedCollector {
	received := prometheus.NewDesc("sonic_pfc_received_frames_total",
		"PFC pause frames received on a port, by priority (the peer pausing this switch port)", []string{"device", "priority"}, nil)
	transmitted := prometheus.NewDesc("sonic_pfc_transmitted_frames_total",
		"PFC pause frames transmitted by a port, by priority (this switch pausing its peer)", []string{"device", "priority"}, nil)
	duration := prometheus.NewDesc("sonic_pfc_pause_duration_microseconds_total",
		"Time a priority was paused, in microseconds (only when the SAI exposes it)", []string{"device", "priority", "direction"}, nil)

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		counters, err := portCounters(ctx, rc)
		if err != nil {
			return nil, err
		}

		devices := make([]string, 0, len(counters))
		for device := range counters {
			devices = append(devices, device)
		}

		sort.Strings(devices)

		var metrics []prometheus.Metric

		for _, device := range devices {
			hash := counters[device]

			for priority := 0; priority < 8; priority++ {
				p := strconv.Itoa(priority)

				for _, item := range []struct {
					field string
					desc  *prometheus.Desc
				}{
					{fmt.Sprintf("SAI_PORT_STAT_PFC_%d_RX_PKTS", priority), received},
					{fmt.Sprintf("SAI_PORT_STAT_PFC_%d_TX_PKTS", priority), transmitted},
				} {
					if value, ok := parseValue("pfc", hash[item.field]); ok {
						metrics = append(metrics, prometheus.MustNewConstMetric(item.desc, prometheus.CounterValue, value, device, p))
					}
				}

				for _, direction := range []string{"RX", "TX"} {
					for _, suffix := range []string{"_PAUSE_DURATION", "_PAUSE_DURATION_US"} {
						raw, present := hash[fmt.Sprintf("SAI_PORT_STAT_PFC_%d_%s%s", priority, direction, suffix)]
						if !present {
							continue
						}

						if value, ok := parseValue("pfc", raw); ok {
							metrics = append(metrics, prometheus.MustNewConstMetric(duration, prometheus.CounterValue, value, device, p,
								map[string]string{"RX": "received", "TX": "transmitted"}[direction]))
						}
					}
				}
			}
		}

		return metrics, nil
	}

	return newCachedCollector("pfc", logger, scrape, received, transmitted, duration)
}
