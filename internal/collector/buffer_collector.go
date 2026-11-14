package collector

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

// NewBufferPoolCollector exports the watermarks of the switch buffer pools (ingress_lossless_pool, ...).
func NewBufferPoolCollector(logger log.Logger) *cachedCollector {
	watermark := prometheus.NewDesc("sonic_buffer_pool_watermark_bytes",
		"Buffer pool watermark in bytes (type: current, user, persistent or periodic)", []string{"pool", "type"}, nil)

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		pools, err := rc.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_BUFFER_POOL_NAME_MAP")
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		keys := make([]string, 0, len(pools)*4)
		for _, oid := range pools {
			keys = append(keys, "COUNTERS:"+oid)
			for _, t := range watermarkTypes {
				keys = append(keys, t+"_WATERMARKS:"+oid)
			}
		}

		hashes, err := rc.PipelineHGetAll(ctx, "COUNTERS_DB", keys)
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		var metrics []prometheus.Metric

		for pool, oid := range pools {
			for _, source := range append([]string{"COUNTERS"}, "USER_WATERMARKS", "PERSISTENT_WATERMARKS", "PERIODIC_WATERMARKS") {
				raw, present := hashes[source+":"+oid]["SAI_BUFFER_POOL_STAT_WATERMARK_BYTES"]
				if !present {
					continue
				}

				label := "current"
				if source != "COUNTERS" {
					label = strings.ToLower(strings.TrimSuffix(source, "_WATERMARKS"))
				}

				if value, ok := parseValue("buffer_pool", raw); ok {
					metrics = append(metrics, prometheus.MustNewConstMetric(watermark, prometheus.GaugeValue, value, pool, label))
				}
			}
		}

		return metrics, nil
	}

	return newCachedCollector("buffer_pool", logger, scrape, watermark)
}

// NewQueueInfoCollector exports the type of every queue (unicast, multicast, all) so that rules and
// dashboards need not assume which queue indexes are multicast.
func NewQueueInfoCollector(logger log.Logger) *cachedCollector {
	info := prometheus.NewDesc("sonic_queue_info", "Queue type, value is always 1", []string{"device", "queue", "type"}, nil)

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		names, err := rc.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_QUEUE_NAME_MAP")
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		types, err := rc.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_QUEUE_TYPE_MAP")
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		var metrics []prometheus.Metric

		for name, oid := range names {
			device, queue, ok := splitDeviceIndex(name)
			if !ok {
				recordError("queue_info", "parse")
				continue
			}

			queueType := strings.ToLower(strings.TrimPrefix(types[oid], "SAI_QUEUE_TYPE_"))
			if queueType == "" {
				queueType = "unknown"
			}

			metrics = append(metrics, prometheus.MustNewConstMetric(info, prometheus.GaugeValue, 1, device, queue, queueType))
		}

		return metrics, nil
	}

	return newCachedCollector("queue_info", logger, scrape, info)
}
