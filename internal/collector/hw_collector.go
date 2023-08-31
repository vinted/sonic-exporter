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

type hwCollector struct {
	hwPsuInfo                 *prometheus.Desc
	hwPsuInputVoltageVolts    *prometheus.Desc
	hwPsuInputCurrentAmperes  *prometheus.Desc
	hwPsuOutputVoltageVolts   *prometheus.Desc
	hwPsuOutputCurrentAmperes *prometheus.Desc
	hwPsuOperationalStatus    *prometheus.Desc
	hwPsuAvailableStatus      *prometheus.Desc
	hwPsuTemperatureCelsius   *prometheus.Desc
	scrapeDuration            *prometheus.Desc
	scrapeCollectorSuccess    *prometheus.Desc
	cachedMetrics             []prometheus.Metric
	lastScrapeTime            time.Time
	logger                    log.Logger
	mu                        sync.Mutex
}

func NewHwCollector(logger log.Logger) *hwCollector {
	const (
		namespace = "sonic"
		subsystem = "hw"
	)

	return &hwCollector{
		hwPsuInfo: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "psu_info"),
			"Non-numeric data about PSU, value is always 1", []string{"slot", "serial", "model_name", "model"}, nil),
		hwPsuInputVoltageVolts: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "psu_input_voltage_volts"),
			"PSU input voltage", []string{"slot"}, nil),
		hwPsuInputCurrentAmperes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "psu_input_current_amperes"),
			"PSU input current", []string{"slot"}, nil),
		hwPsuOutputVoltageVolts: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "psu_output_voltage_volts"),
			"PSU output voltage", []string{"slot"}, nil),
		hwPsuOutputCurrentAmperes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "psu_output_current_amperes"),
			"PSU output current", []string{"slot"}, nil),
		hwPsuOperationalStatus: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "psu_operational_status"),
			"PSU operational status: 0(DOWN), 1(UP)", []string{"slot"}, nil),
		hwPsuAvailableStatus: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "psu_available_status"),
			"PSU availability status: not plugged in - 0, plugged in - 1", []string{"slot"}, nil),
		hwPsuTemperatureCelsius: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "psu_temperature_celsius"),
			"PSU temperature", []string{"slot"}, nil),
		scrapeDuration: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "scrape_duration_seconds"),
			"Time it took for prometheus to scrape sonic hw metrics", nil, nil),
		scrapeCollectorSuccess: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "collector_success"),
			"Whether hw collector succeeded", nil, nil),
		logger: logger,
	}
}

func (collector *hwCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.hwPsuInfo
	ch <- collector.hwPsuInputVoltageVolts
	ch <- collector.hwPsuInputCurrentAmperes
	ch <- collector.hwPsuOutputVoltageVolts
	ch <- collector.hwPsuOutputCurrentAmperes
	ch <- collector.hwPsuOperationalStatus
	ch <- collector.hwPsuAvailableStatus
	ch <- collector.hwPsuTemperatureCelsius

}

func (collector *hwCollector) Collect(ch chan<- prometheus.Metric) {
	const cacheDuration = 15 * time.Second

	scrapeSuccess := 1.0

	var ctx = context.Background()

	collector.mu.Lock()
	defer collector.mu.Unlock()

	if time.Since(collector.lastScrapeTime) < cacheDuration {
		// Return cached metrics without making redis calls
		level.Info(collector.logger).Log("msg", "Returning hw metrics from cache")

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

	for _, cachedMetric := range collector.cachedMetrics {
		ch <- cachedMetric
	}

	ch <- prometheus.MustNewConstMetric(collector.scrapeCollectorSuccess, prometheus.GaugeValue, scrapeSuccess)
}

func (collector *hwCollector) scrapeMetrics(ctx context.Context) error {
	level.Info(collector.logger).Log("msg", "Starting hw metric scrape")
	scrapeTime := time.Now()

	redisClient, err := redis.NewClient()
	if err != nil {
		return fmt.Errorf("redis client initialization failed: %w", err)
	}

	// Reset metrics
	collector.cachedMetrics = []prometheus.Metric{}

	err = collector.collectPsuInfo(ctx, redisClient)
	if err != nil {
		return fmt.Errorf("hw psu info collection failed: %w", err)
	}

	level.Info(collector.logger).Log("msg", "Ending hw metric scrape")

	collector.lastScrapeTime = time.Now()
	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.scrapeDuration, prometheus.GaugeValue, time.Since(scrapeTime).Seconds(),
	))
	return nil
}

func (collector *hwCollector) collectPsuInfo(ctx context.Context, redisClient redis.Client) error {
	const psuKeyPattern string = "PSU_INFO|PSU*"

	psuKeys, err := redisClient.KeysFromDb(ctx, "STATE_DB", psuKeyPattern)
	if err != nil {
		return err
	}

	for _, psuKey := range psuKeys {
		available_status := 0.0
		operational_status := 0.0
		psuId := strings.Split(psuKey, " ")[1]

		data, err := redisClient.HgetAllFromDb(ctx, "STATE_DB", psuKey)
		if err != nil {
			return err
		}

		serial := data["serial"]
		modelName := data["name"]
		model := data["model"]

		collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
			collector.hwPsuInfo, prometheus.GaugeValue, 1, psuId, serial, modelName, model,
		))

		if data["status"] == "true" {
			operational_status = 1.0
		}
		collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
			collector.hwPsuOperationalStatus, prometheus.GaugeValue, operational_status, psuId,
		))

		if data["presence"] == "true" {
			available_status = 1.0
		}
		collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
			collector.hwPsuAvailableStatus, prometheus.GaugeValue, available_status, psuId,
		))

		// voltage, amperage and temperature metrics are appended only if values can be parsed
		inVolts, err := strconv.ParseFloat(data["input_voltage"], 64)
		if err == nil {
			collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
				collector.hwPsuInputVoltageVolts, prometheus.GaugeValue, inVolts, psuId,
			))
		}

		inAmperes, err := strconv.ParseFloat(data["input_current"], 64)
		if err == nil {
			collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
				collector.hwPsuInputCurrentAmperes, prometheus.GaugeValue, inAmperes, psuId,
			))
		}

		outVolts, err := strconv.ParseFloat(data["output_voltage"], 64)
		if err == nil {
			collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
				collector.hwPsuOutputVoltageVolts, prometheus.GaugeValue, outVolts, psuId,
			))
		}

		outAmperes, err := strconv.ParseFloat(data["output_current"], 64)
		if err == nil {
			collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
				collector.hwPsuOutputCurrentAmperes, prometheus.GaugeValue, outAmperes, psuId,
			))
		}

		temp, err := strconv.ParseFloat(data["temp"], 64)
		if err == nil {
			collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
				collector.hwPsuTemperatureCelsius, prometheus.GaugeValue, temp, psuId,
			))
		}
	}

	return nil
}
