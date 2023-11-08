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

type crmCollector struct {
	crmResourceAvailable   *prometheus.Desc
	crmResourceUsed        *prometheus.Desc
	scrapeDuration         *prometheus.Desc
	scrapeCollectorSuccess *prometheus.Desc
	cachedMetrics          []prometheus.Metric
	lastScrapeTime         time.Time
	logger                 log.Logger
	mu                     sync.Mutex
}

func NewCrmCollector(logger log.Logger) *crmCollector {
	const (
		namespace = "sonic"
		subsystem = "crm"
	)

	return &crmCollector{
		crmResourceAvailable: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "crm_resource_available"),
			"Maximum available value for a resource", []string{"resource"}, nil),
		crmResourceUsed: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "crm_resource_used"),
			"Used value for a resource", []string{"resource"}, nil),
		scrapeDuration: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "scrape_duration_seconds"),
			"Time it took for prometheus to scrape sonic crm metrics", nil, nil),
		scrapeCollectorSuccess: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "collector_success"),
			"Whether crm collector succeeded", nil, nil),
		logger: logger,
	}
}

func (collector *crmCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.crmResourceAvailable
	ch <- collector.crmResourceUsed
	ch <- collector.scrapeDuration
	ch <- collector.scrapeCollectorSuccess
}

func (collector *crmCollector) Collect(ch chan<- prometheus.Metric) {
	const cacheDuration = 15 * time.Second

	scrapeSuccess := 1.0

	var ctx = context.Background()

	collector.mu.Lock()
	defer collector.mu.Unlock()

	if time.Since(collector.lastScrapeTime) < cacheDuration {
		// Return cached metrics without making redis calls
		level.Info(collector.logger).Log("msg", "Returning crm metrics from cache")

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

func (collector *crmCollector) scrapeMetrics(ctx context.Context) error {
	level.Info(collector.logger).Log("msg", "Starting crm metric scrape")
	scrapeTime := time.Now()

	redisClient, err := redis.NewClient()
	if err != nil {
		return fmt.Errorf("redis client initialization failed: %w", err)
	}

	// Reset metrics
	collector.cachedMetrics = []prometheus.Metric{}

	crmStats, err := redisClient.HgetAllFromDb(ctx, "COUNTERS_DB", "CRM:STATS")
	if err != nil {
		return fmt.Errorf("redis read failed: %w", err)
	}

	err = collector.collectCrmStatsCounters(ctx, crmStats)
	if err != nil {
		return fmt.Errorf("crm stats collection failed: %w", err)
	}

	level.Info(collector.logger).Log("msg", "Ending crm metric scrape")
	collector.lastScrapeTime = time.Now()
	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.scrapeDuration, prometheus.GaugeValue, time.Since(scrapeTime).Seconds(),
	))
	return nil
}

func (collector *crmCollector) collectCrmStatsCounters(ctx context.Context, crmStats map[string]string) error {
	for stat, value := range crmStats {
		parsedValue, err := parseFloat(value)
		if err != nil {
			return fmt.Errorf("value parse failed: %w", err)
		}

		if strings.HasSuffix(stat, "available") {
			label := strings.TrimSuffix(strings.TrimPrefix(stat, "crm_stats_"), "_available")
			collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
				collector.crmResourceAvailable, prometheus.GaugeValue, parsedValue, label,
			))
		}

		if strings.HasSuffix(stat, "used") {
			label := strings.TrimSuffix(strings.TrimPrefix(stat, "crm_stats_"), "_used")
			collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
				collector.crmResourceUsed, prometheus.GaugeValue, parsedValue, label,
			))
		}
	}

	return nil
}
