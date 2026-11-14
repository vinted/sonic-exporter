package collector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

type queueCollector struct {
	queuePackets              *prometheus.Desc
	queueBytes                *prometheus.Desc
	queueDroppedPackets       *prometheus.Desc
	queueDroppedBytes         *prometheus.Desc
	queueSharedWatermarkBytes *prometheus.Desc
	queueWatermarksBytes      *prometheus.Desc
	scrapeDuration            *prometheus.Desc
	lastRefresh               *prometheus.Desc
	scrapeCollectorSuccess    *prometheus.Desc
	cachedMetrics             []prometheus.Metric
	lastScrapeTime            time.Time
	logger                    log.Logger
	mu                        sync.Mutex
}

func NewQueueCollector(logger log.Logger) *queueCollector {
	const (
		namespace = "sonic"
		subsystem = "queue"
	)

	return &queueCollector{
		queuePackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "packets_total"),
			"Number of packets in a queue", []string{"device", "queue"}, nil),
		queueBytes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "bytes_total"),
			"Number of bytes in a queue", []string{"device", "queue"}, nil),
		queueDroppedPackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "dropped_packets_total"),
			"Number of dropped packets in a queue", []string{"device", "queue"}, nil),
		queueDroppedBytes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "dropped_bytes_total"),
			"Number of dropped bytes in a queue", []string{"device", "queue"}, nil),
		queueSharedWatermarkBytes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "shared_watermark_bytes_total"),
			"Number of shared watermark bytes in a queue", []string{"device", "queue"}, nil),
		queueWatermarksBytes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "watermark_bytes_total"),
			"Network device property: watermarks of queue", []string{"device", "queue", "type", "watermark"}, nil),
		scrapeDuration: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "scrape_duration_seconds"),
			"Time it took for prometheus to scrape sonic queue metrics", nil, nil),
		scrapeCollectorSuccess: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "collector_success"),
			"Whether queue collector succeeded", nil, nil),
		lastRefresh: newRefreshDesc("queue"),
		logger:      logger,
	}
}

func (collector *queueCollector) Collect(ch chan<- prometheus.Metric) {
	const cacheDuration = 15 * time.Second

	scrapeSuccess := 1.0

	var ctx = context.Background()

	collector.mu.Lock()
	defer collector.mu.Unlock()

	if time.Since(collector.lastScrapeTime) < cacheDuration {
		// Return cached metrics without making redis calls
		level.Info(collector.logger).Log("msg", "Returning queue metrics from cache")

		for _, metric := range collector.cachedMetrics {
			ch <- metric
		}
		return
	}

	err := collector.scrapeMetrics(ctx)
	if err != nil {
		scrapeSuccess = 0
		recordError("queue", "redis")
		level.Error(collector.logger).Log("err", err)
		collector.lastScrapeTime = time.Now() // cache failures too
	}
	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.scrapeCollectorSuccess, prometheus.GaugeValue, scrapeSuccess,
	))

	for _, cachedMetric := range collector.cachedMetrics {
		ch <- cachedMetric
	}
}

func (collector *queueCollector) scrapeMetrics(ctx context.Context) error {
	level.Info(collector.logger).Log("msg", "Starting queue metric scrape")
	scrapeTime := time.Now()

	redisClient, err := redis.NewClient()
	if err != nil {
		return fmt.Errorf("redis client initialization failed: %w", err)
	}

	defer redisClient.Close()

	// Reset metrics
	collector.cachedMetrics = []prometheus.Metric{}

	queues, err := redisClient.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_QUEUE_NAME_MAP")
	if err != nil {
		return fmt.Errorf("redis read failed: %w", err)
	}

	keys := make([]string, 0, len(queues)*4)
	for _, oid := range queues {
		keys = append(keys, "COUNTERS:"+oid)
		for _, watermarkType := range watermarkTypes {
			keys = append(keys, watermarkType+"_WATERMARKS:"+oid)
		}
	}

	hashes, err := redisClient.PipelineHGetAll(ctx, "COUNTERS_DB", keys)
	if err != nil {
		return fmt.Errorf("redis read failed: %w", err)
	}

	for queue, oid := range queues {
		interfaceName, queueNumber, ok := splitDeviceIndex(queue)
		if !ok {
			recordError("queue", "parse")
			continue
		}

		collector.collectQueueCounters(interfaceName, queueNumber, hashes["COUNTERS:"+oid])
		collector.collectQueueWatermarks(interfaceName, queueNumber, oid, hashes)
	}

	level.Info(collector.logger).Log("msg", "Ending queue metric scrape")

	collector.lastScrapeTime = time.Now()
	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.scrapeDuration, prometheus.GaugeValue, time.Since(scrapeTime).Seconds(),
	), prometheus.MustNewConstMetric(
		collector.lastRefresh, prometheus.GaugeValue, float64(time.Now().Unix()),
	))
	return nil
}

func (collector *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.queuePackets
	ch <- collector.queueBytes
	ch <- collector.queueDroppedPackets
	ch <- collector.queueDroppedBytes
	ch <- collector.queueSharedWatermarkBytes
	ch <- collector.queueWatermarksBytes
	ch <- collector.scrapeDuration
	ch <- collector.scrapeCollectorSuccess
	ch <- collector.lastRefresh
}

var watermarkTypes = []string{"USER", "PERSISTENT", "PERIODIC"}

// collectQueueCounters exports the queue counters. A value that cannot be parsed is counted in
// sonic_exporter_errors_total and only that series is skipped.
func (collector *queueCollector) collectQueueCounters(interfaceName, queueNumber string, counters map[string]string) {
	series := []struct {
		field string
		desc  *prometheus.Desc
	}{
		{"SAI_QUEUE_STAT_PACKETS", collector.queuePackets},
		{"SAI_QUEUE_STAT_BYTES", collector.queueBytes},
		{"SAI_QUEUE_STAT_DROPPED_PACKETS", collector.queueDroppedPackets},
		{"SAI_QUEUE_STAT_DROPPED_BYTES", collector.queueDroppedBytes},
		{"SAI_QUEUE_STAT_SHARED_WATERMARK_BYTES", collector.queueSharedWatermarkBytes},
	}

	for _, item := range series {
		value, ok := parseValue("queue", counters[item.field])
		if !ok {
			continue
		}

		collector.cachedMetrics = append(collector.cachedMetrics,
			prometheus.MustNewConstMetric(item.desc, prometheus.CounterValue, value, interfaceName, queueNumber),
		)
	}
}

func (collector *queueCollector) collectQueueWatermarks(interfaceName, queueNumber, queueOid string, hashes map[string]map[string]string) {
	for _, watermarkType := range watermarkTypes {
		for watermarkKey, raw := range hashes[watermarkType+"_WATERMARKS:"+queueOid] {
			value, ok := parseValue("queue", raw)
			if !ok {
				continue
			}

			watermarkLabel := strings.TrimPrefix(watermarkKey, "SAI_QUEUE_STAT_")
			watermarkLabel = strings.TrimPrefix(watermarkLabel, "SAI_INGRESS_PRIORITY_GROUP_STAT_")
			watermarkLabel = strings.TrimSuffix(watermarkLabel, "_WATERMARK_BYTES")

			collector.cachedMetrics = append(collector.cachedMetrics,
				prometheus.MustNewConstMetric(
					collector.queueWatermarksBytes, prometheus.CounterValue, value,
					interfaceName, queueNumber, strings.ToLower(watermarkType), strings.ToLower(watermarkLabel),
				),
			)
		}
	}
}
