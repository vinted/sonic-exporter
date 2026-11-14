package collector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

const cacheDuration = 15 * time.Second

// scrapeErrors counts values and redis calls that a collector had to skip, by collector and reason.
// A skipped value no longer aborts the whole collector.
var scrapeErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "sonic_exporter_errors_total",
	Help: "Number of errors while collecting, by collector and reason (parse, redis).",
}, []string{"collector", "reason"})

func recordError(collector, reason string) {
	scrapeErrors.WithLabelValues(collector, reason).Inc()
}

// parseValue parses a redis counter value. A missing value is 0 (as before); an unparsable value is
// counted and reported as not ok so that the caller skips only that series.
func parseValue(collector, str string) (float64, bool) {
	value, err := parseFloat(str)
	if err != nil {
		recordError(collector, "parse")
		return 0, false
	}

	return value, true
}

// splitDeviceIndex splits "Ethernet0:3" into "Ethernet0" and "3".
func splitDeviceIndex(name string) (string, string, bool) {
	parts := strings.SplitN(name, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	return parts[0], parts[1], true
}

// parseIntList parses "3,4" or "3-4" or "0-1,5" into integers; invalid items are ignored.
func parseIntList(spec string) []int {
	var out []int

	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		if lo, hi, found := strings.Cut(item, "-"); found {
			a, errA := strconv.Atoi(lo)
			b, errB := strconv.Atoi(hi)

			if errA != nil || errB != nil || b < a || b-a > 63 {
				continue
			}

			for i := a; i <= b; i++ {
				out = append(out, i)
			}

			continue
		}

		if n, err := strconv.Atoi(item); err == nil {
			out = append(out, n)
		}
	}

	return out
}

// stripBrackets turns "[BUFFER_PROFILE|ingress_lossless_profile]" into "ingress_lossless_profile".
func stripBrackets(ref string) string {
	ref = strings.Trim(ref, "[]")
	if idx := strings.LastIndex(ref, "|"); idx >= 0 {
		return ref[idx+1:]
	}

	return ref
}

// scrapeFunc reads redis and returns the metrics. On a partial failure it returns the metrics it has
// together with the error; the collector then reports collector_success 0.
type scrapeFunc func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error)

// cachedCollector is the shared plumbing of the collectors added after the first four: a 15 s cache that
// also caches failures (so a sick switch is not hit on every scrape), duration and success metrics.
type cachedCollector struct {
	name        string
	descs       []*prometheus.Desc
	success     *prometheus.Desc
	seconds     *prometheus.Desc
	refreshDesc *prometheus.Desc
	scrape      scrapeFunc
	logger      log.Logger

	mu     sync.Mutex
	last   time.Time
	cached []prometheus.Metric
}

func newCachedCollector(name string, logger log.Logger, scrape scrapeFunc, descs ...*prometheus.Desc) *cachedCollector {
	return &cachedCollector{
		name:   name,
		descs:  descs,
		logger: logger,
		scrape: scrape,
		success: prometheus.NewDesc(prometheus.BuildFQName("sonic", name, "collector_success"),
			fmt.Sprintf("Whether %s collector succeeded", name), nil, nil),
		seconds: prometheus.NewDesc(prometheus.BuildFQName("sonic", name, "scrape_duration_seconds"),
			fmt.Sprintf("Time it took to scrape sonic %s metrics", name), nil, nil),
		refreshDesc: newRefreshDesc(name),
	}
}

// newRefreshDesc describes the time of the last redis read of a collector. The exporter caches every collector for 15 s, so a
// value read from the exporter can be that old; this timestamp lets rules and people see the age.
func newRefreshDesc(name string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName("sonic", name, "last_refresh_timestamp_seconds"),
		fmt.Sprintf("Unix time of the last redis read of the %s collector", name), nil, nil)
}

func (c *cachedCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descs {
		ch <- desc
	}

	ch <- c.success
	ch <- c.seconds
	ch <- c.refreshDesc
}

func (c *cachedCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.last) >= cacheDuration {
		c.refresh()
	}

	for _, metric := range c.cached {
		ch <- metric
	}
}

func (c *cachedCollector) refresh() {
	start := time.Now()
	successValue := 1.0

	rc, err := redis.NewClient()
	if err != nil {
		recordError(c.name, "redis")
		level.Error(c.logger).Log("collector", c.name, "err", err)

		c.cached = []prometheus.Metric{prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 0)}
		c.last = time.Now()

		return
	}

	defer rc.Close()

	metrics, err := c.scrape(context.Background(), rc)
	if err != nil {
		successValue = 0

		recordError(c.name, "redis")
		level.Error(c.logger).Log("collector", c.name, "err", err)
	}

	metrics = append(metrics,
		prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, successValue),
		prometheus.MustNewConstMetric(c.seconds, prometheus.GaugeValue, time.Since(start).Seconds()),
		prometheus.MustNewConstMetric(c.refreshDesc, prometheus.GaugeValue, float64(time.Now().Unix())),
	)

	c.cached = metrics
	c.last = time.Now() // failures are cached too
}
