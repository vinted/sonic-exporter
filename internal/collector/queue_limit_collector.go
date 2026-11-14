package collector

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/kingpin/v2"
	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

var (
	queueLimitDefaultBytes = kingpin.Flag("collector.queue_limit.default-bytes",
		"Limit in bytes assumed for the queues listed in --collector.queue_limit.default-queues that have no BUFFER_QUEUE profile (0 = none). "+
			"Use the observed per-queue ceiling of the platform (for example the watermark every dropping port peaks at).").Default("0").Int64()
	queueLimitDefaultQueues = kingpin.Flag("collector.queue_limit.default-queues",
		"Queues that get the default limit.").Default("3,4").String()
)

// NewQueueLimitCollector exports the buffer limit of egress queues so that a rule can alert on watermark / limit before the queue drops.
// The limit comes from the BUFFER_QUEUE profile (size, or static_th when size is 0); queues without a profile get the optional default.
func NewQueueLimitCollector(logger log.Logger) *cachedCollector {
	limit := prometheus.NewDesc("sonic_queue_limit_bytes",
		"Buffer limit of an egress queue in bytes (source: profile = BUFFER_QUEUE profile, default = configured default)", []string{"device", "queue", "source"}, nil)

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		names, err := rc.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_QUEUE_NAME_MAP")
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		hashes, err := configKeys(ctx, rc, "BUFFER_QUEUE|*", "BUFFER_PROFILE|*")
		if err != nil {
			return nil, err
		}

		var metrics []prometheus.Metric

		seen := map[string]bool{}

		for key, hash := range hashes {
			parts := strings.Split(key, "|")
			if parts[0] != "BUFFER_QUEUE" || len(parts) != 3 {
				continue
			}

			profile := hashes["BUFFER_PROFILE|"+stripBrackets(hash["profile"])]

			size, ok := parseValue("queue_limit", profile["size"])
			if !ok {
				continue
			}

			if size == 0 {
				size, _ = parseValue("queue_limit", profile["static_th"])
			}

			if size == 0 {
				continue
			}

			for _, q := range parseIntList(parts[2]) {
				seen[parts[1]+":"+strconv.Itoa(q)] = true

				metrics = append(metrics, prometheus.MustNewConstMetric(limit, prometheus.GaugeValue, size, parts[1], strconv.Itoa(q), "profile"))
			}
		}

		if *queueLimitDefaultBytes > 0 {
			defaults := map[string]bool{}
			for _, q := range parseIntList(*queueLimitDefaultQueues) {
				defaults[strconv.Itoa(q)] = true
			}

			for name := range names {
				device, queue, ok := splitDeviceIndex(name)
				if ok && defaults[queue] && !seen[device+":"+queue] {
					metrics = append(metrics, prometheus.MustNewConstMetric(limit, prometheus.GaugeValue, float64(*queueLimitDefaultBytes), device, queue, "default"))
				}
			}
		}

		return metrics, nil
	}

	return newCachedCollector("queue_limit", logger, scrape, limit)
}
