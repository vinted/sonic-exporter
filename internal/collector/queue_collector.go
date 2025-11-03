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
		logger: logger,
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
		level.Error(collector.logger).Log("err", err)
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

	for queue := range queues {
		interfaceName := strings.Split(queue, ":")[0]
		queueNumber := strings.Split(queue, ":")[1]

		counterKey := fmt.Sprintf("COUNTERS:%s", queues[queue])

		err := collector.collectQueueCounters(ctx, redisClient, interfaceName, queueNumber, counterKey)
		if err != nil {
			return fmt.Errorf("queue counters collection failed: %w", err)
		}

		err = collector.collectQueueWatermarks(ctx, redisClient, interfaceName, queueNumber, queues[queue])
		if err != nil {
			return fmt.Errorf("queue watermarks collection failed: %w", err)
		}
	}

	level.Info(collector.logger).Log("msg", "Ending queue metric scrape")

	collector.lastScrapeTime = time.Now()
	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.scrapeDuration, prometheus.GaugeValue, time.Since(scrapeTime).Seconds(),
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
}

func (collector *queueCollector) collectQueueCounters(ctx context.Context, redisClient redis.Client, interfaceName, queueNumber, counterKey string) error {
	var counters map[string]string

	// Retrieve packet counters from redis database
	counters, err := redisClient.HgetAllFromDb(ctx, "COUNTERS_DB", counterKey)
	if err != nil {
		return fmt.Errorf("redis read failed: %w", err)
	}

	packets, err := parseFloat(counters["SAI_QUEUE_STAT_PACKETS"])
	if err != nil {
		return fmt.Errorf("value parse failed: %w", err)
	}

	collector.cachedMetrics = append(collector.cachedMetrics,
		prometheus.MustNewConstMetric(
			collector.queuePackets, prometheus.CounterValue, packets, interfaceName, queueNumber,
		),
	)

	bytes, err := parseFloat(counters["SAI_QUEUE_STAT_BYTES"])
	if err != nil {
		return fmt.Errorf("value parse failed: %w", err)
	}

	collector.cachedMetrics = append(collector.cachedMetrics,
		prometheus.MustNewConstMetric(
			collector.queueBytes, prometheus.CounterValue, bytes, interfaceName, queueNumber,
		),
	)

	droppedPackets, err := parseFloat(counters["SAI_QUEUE_STAT_DROPPED_PACKETS"])
	if err != nil {
		return fmt.Errorf("value parse failed: %w", err)
	}

	collector.cachedMetrics = append(collector.cachedMetrics,
		prometheus.MustNewConstMetric(
			collector.queueDroppedPackets, prometheus.CounterValue, droppedPackets, interfaceName, queueNumber,
		),
	)

	droppedBytes, err := parseFloat(counters["SAI_QUEUE_STAT_DROPPED_BYTES"])
	if err != nil {
		return fmt.Errorf("value parse failed: %w", err)
	}

	collector.cachedMetrics = append(collector.cachedMetrics,
		prometheus.MustNewConstMetric(
			collector.queueDroppedBytes, prometheus.CounterValue, droppedBytes, interfaceName, queueNumber,
		),
	)

	sharedWatermarkBytes, err := parseFloat(counters["SAI_QUEUE_STAT_SHARED_WATERMARK_BYTES"])
	if err != nil {
		return fmt.Errorf("value parse failed: %w", err)
	}

	collector.cachedMetrics = append(collector.cachedMetrics,
		prometheus.MustNewConstMetric(
			collector.queueSharedWatermarkBytes, prometheus.CounterValue, sharedWatermarkBytes, interfaceName, queueNumber,
		),
	)

	return nil
}

func (collector *queueCollector) collectQueueWatermarks(ctx context.Context, redisClient redis.Client, interfaceName, queueNumber, queueName string) error {
	var watermarkValue float64
	for _, watermarkType := range []string{"USER", "PERSISTENT", "PERIODIC"} {
		watermarksKey := fmt.Sprintf("%s_WATERMARKS:%s", watermarkType, queueName)
		watermarks, err := redisClient.HgetAllFromDb(ctx, "COUNTERS_DB", watermarksKey)
		if err != nil {
			return fmt.Errorf("redis read failed: %w", err)
		}

		for watermarkKey, value := range watermarks {
			watermarkValue, err = parseFloat(value)
			if err != nil {
				return fmt.Errorf("value parse failed: %w", err)
			}

			prefixes := []string{"SAI_QUEUE_STAT_", "SAI_INGRESS_PRIORITY_GROUP_STAT_"}
			suffixes := []string{"_WATERMARK_BYTES"}
			watermarkLabel := watermarkKey
			for _, prefix := range prefixes {
				watermarkLabel = strings.TrimPrefix(watermarkLabel, prefix)
			}

			for _, suffix := range suffixes {
				watermarkLabel = strings.TrimSuffix(watermarkLabel, suffix)
			}

			collector.cachedMetrics = append(collector.cachedMetrics,
				prometheus.MustNewConstMetric(
					collector.queueWatermarksBytes, prometheus.CounterValue, watermarkValue,
					interfaceName, queueNumber, strings.ToLower(watermarkType), strings.ToLower(watermarkLabel),
				),
			)
		}
	}

	return nil
}
