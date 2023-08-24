package collector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/promlog"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

const (
	namespace     = "sonic"
	subsystem     = "interface"
	cacheDuration = 15 * time.Second
)

var (
	ctx    = context.Background()
	logger = promlog.New(&promlog.Config{})
)

var metricsCtx InterfaceMetrics

type InterfaceMetrics struct {
	metrics        []prometheus.Metric
	lastScrapeTime time.Time
}

var interfacePacketSizeKeyMap = map[string]string{
	"64":    "SAI_PORT_STAT_ETHER_%s_PKTS_64_OCTETS",
	"127":   "SAI_PORT_STAT_ETHER_%s_PKTS_65_TO_127_OCTETS",
	"255":   "SAI_PORT_STAT_ETHER_%s_PKTS_128_TO_255_OCTETS",
	"511":   "SAI_PORT_STAT_ETHER_%s_PKTS_256_TO_511_OCTETS",
	"1023":  "SAI_PORT_STAT_ETHER_%s_PKTS_512_TO_1023_OCTETS",
	"1518":  "SAI_PORT_STAT_ETHER_%s_PKTS_1024_TO_1518_OCTETS",
	"2047":  "SAI_PORT_STAT_ETHER_%s_PKTS_1519_TO_2047_OCTETS",
	"4095":  "SAI_PORT_STAT_ETHER_%s_PKTS_2048_TO_4095_OCTETS",
	"9216":  "SAI_PORT_STAT_ETHER_%s_PKTS_4096_TO_9216_OCTETS",
	"16383": "SAI_PORT_STAT_ETHER_%s_PKTS_9217_TO_16383_OCTETS",
}

var interfaceErrorTypeMap = map[string]map[string]string{
	"in": {
		"error":   "SAI_PORT_STAT_IF_IN_ERRORS",
		"discard": "SAI_PORT_STAT_IF_IN_DISCARDS",
		"drop":    "SAI_PORT_STAT_IN_DROPPED_PKTS",
		"pause":   "SAI_PORT_STAT_PAUSE_RX_PKTS",
	},
	"out": {
		"error":   "SAI_PORT_STAT_IF_OUT_ERRORS",
		"discard": "SAI_PORT_STAT_IF_OUT_DISCARDS",
		"pause":   "SAI_PORT_STAT_PAUSE_TX_PKTS",
	},
}

const (
	interfaceByteCountKey   = "SAI_PORT_STAT_IF_%s_OCTETS"
	interfacePacketCountKey = "SAI_PORT_STAT_IF_%s_%s_PKTS"
)

type interfaceCollector struct {
	interfaceInfo                    *prometheus.Desc
	interfaceMtu                     *prometheus.Desc
	interfaceSpeed                   *prometheus.Desc
	interfaceTransmitEthernetPackets *prometheus.Desc
	interfaceTransmitPackets         *prometheus.Desc
	interfaceTransmitBytes           *prometheus.Desc
	interfaceTransmitErrs            *prometheus.Desc
	interfaceReceiveEthernetPackets  *prometheus.Desc
	interfaceReceivePackets          *prometheus.Desc
	interfaceReceivedBytes           *prometheus.Desc
	interfaceReceiveErrs             *prometheus.Desc
	scrapeDuration                   *prometheus.Desc
	scrapeCollectorSuccess           *prometheus.Desc
}

func NewInterfaceCollector() *interfaceCollector {
	return &interfaceCollector{
		interfaceInfo: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "info"),
			"Non-numeric data about interface, value is always 1", []string{"device", "alias", "index", "description"}, nil),
		interfaceMtu: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "mtu_bytes"),
			"Network device property: mtu_bytes", []string{"device"}, nil),
		interfaceSpeed: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "speed_bytes"),
			"Network device property: speed_bytes", []string{"device"}, nil),
		interfaceTransmitEthernetPackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transmit_ethernet_packets"),
			"Number of ethernet packets transmitted on an interface", []string{"device", "size"}, nil),
		interfaceTransmitPackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transmit_packets"),
			"Number of packets transmitted on an interface", []string{"device", "method"}, nil),
		interfaceTransmitErrs: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transmit_errs"),
			"Number of transmit errs on an interface", []string{"device", "type"}, nil),
		interfaceTransmitBytes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transmit_bytes"),
			"Number of bytes transmitted on an interface", []string{"device"}, nil),
		interfaceReceiveEthernetPackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "receivd_ethernet_packets"),
			"Number of ethernet packets received on an interface", []string{"device", "size"}, nil),
		interfaceReceivePackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "receive_packets"),
			"Number of packets received on an interface", []string{"device", "method"}, nil),
		interfaceReceiveErrs: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "receive_errs"),
			"Number of receive errs on an interface", []string{"device", "type"}, nil),
		interfaceReceivedBytes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "receive_bytes"),
			"Number of bytes received on an interface", []string{"device"}, nil),
		scrapeDuration: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "scrape_duration_seconds"),
			"Time it took for prometheus to scrape sonic metrics", nil, nil),
		scrapeCollectorSuccess: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "collector_success"),
			"Whether interface collector succeeded", nil, nil),
	}
}

func (collector *interfaceCollector) Collect(ch chan<- prometheus.Metric) {
	scrapeTime := time.Now()
	// collector.scrapeMetrics(ch)
	if time.Since(metricsCtx.lastScrapeTime) < cacheDuration {
		// Return cached metrics without making redis calls
		level.Info(logger).Log("msg", "Returning metrics from cache")

		for _, metric := range metricsCtx.metrics {
			ch <- metric
		}
		ch <- prometheus.MustNewConstMetric(collector.scrapeDuration, prometheus.GaugeValue, time.Since(scrapeTime).Seconds())
		return
	}

	collector.scrapeMetrics()

	for _, cachedMetric := range metricsCtx.metrics {
		ch <- cachedMetric
	}

	ch <- prometheus.MustNewConstMetric(collector.scrapeDuration, prometheus.GaugeValue, time.Since(scrapeTime).Seconds())
}

func (collector *interfaceCollector) scrapeMetrics() {
	level.Info(logger).Log("msg", "Starting metric scrape")
	var scrapeSuccess = 1.0

	redisClient, err := redis.NewClient()
	if err != nil {
		level.Error(logger).Log("msg", "Redis client initialization failed", "err", err)
	}

	// Reset metrics
	metricsCtx.metrics = []prometheus.Metric{}

	ports, err := redisClient.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_PORT_NAME_MAP")
	if err != nil {
		level.Error(logger).Log("msg", "Redis read failed", "err", err)
		scrapeSuccess = 0
	}

	for port := range ports {
		counterKey := fmt.Sprintf("COUNTERS:%s", ports[port])

		interfaceCounters, err := collector.collectInterfaceCounters(redisClient, port, counterKey)
		if err != nil {
			level.Error(logger).Log("msg", "Interface counters collection failed", "err", err)
			scrapeSuccess = 0
		}

		interfaceInfo, err := collector.collectInterfaceInfo(redisClient, port)
		if err != nil {
			level.Error(logger).Log("msg", "Interface info collection failed", "err", err)
			scrapeSuccess = 0
		}

		metricsCtx.metrics = append(metricsCtx.metrics, interfaceCounters...)
		metricsCtx.metrics = append(metricsCtx.metrics, interfaceInfo...)
	}

	metricsCtx.metrics = append(metricsCtx.metrics, prometheus.MustNewConstMetric(
		collector.scrapeCollectorSuccess, prometheus.GaugeValue, scrapeSuccess,
	))
	level.Info(logger).Log("msg", "Ending metric scrape")

	metricsCtx.lastScrapeTime = time.Now()
}

func (collector *interfaceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.interfaceInfo
	ch <- collector.interfaceMtu
	ch <- collector.interfaceSpeed
	ch <- collector.interfaceTransmitEthernetPackets
	ch <- collector.interfaceTransmitPackets
	ch <- collector.interfaceTransmitErrs
	ch <- collector.interfaceTransmitBytes
	ch <- collector.interfaceReceiveEthernetPackets
	ch <- collector.interfaceReceivePackets
	ch <- collector.interfaceReceiveErrs
	ch <- collector.interfaceReceivedBytes
	ch <- collector.scrapeDuration
	ch <- collector.scrapeCollectorSuccess
}

func (collector *interfaceCollector) collectInterfaceCounters(redisClient redis.Client, interfaceName, counterKey string) ([]prometheus.Metric, error) {
	var (
		counters map[string]string
	)

	collectedMetrics := make([]prometheus.Metric, 0) // Initialize an empty slice

	// Retrieve packet counters from redis database
	counters, err := redisClient.HgetAllFromDb(ctx, "COUNTERS_DB", counterKey)
	if err != nil {
		level.Error(logger).Log("msg", "Redis read failed", "err", err)
		return nil, err
	}

	byteCounters, err := collector.collectInterfaceByteCounters(interfaceName, counters)
	if err != nil {
		level.Error(logger).Log("msg", "byte counters collection failed", "err", err)
		return nil, err
	}
	collectedMetrics = append(collectedMetrics, byteCounters...)

	errCounters, err := collector.collectInterfaceErrCounters(interfaceName, counters)
	if err != nil {
		level.Error(logger).Log("msg", "err counters collection failed", "err", err)
		return nil, err
	}
	collectedMetrics = append(collectedMetrics, errCounters...)

	packetCounters, err := collector.collectInterfacePacketCounters(interfaceName, counters)
	if err != nil {
		level.Error(logger).Log("msg", "packet counters collection failed", "err", err)
		return nil, err
	}
	collectedMetrics = append(collectedMetrics, packetCounters...)

	packetSizeCounters, err := collector.collectInterfacePacketSizeCounters(interfaceName, counters)
	if err != nil {
		level.Error(logger).Log("msg", "packet size counters collection failed", "err", err)
		return nil, err
	}
	collectedMetrics = append(collectedMetrics, packetSizeCounters...)

	return collectedMetrics, nil

}

func (collector *interfaceCollector) collectInterfaceInfo(redisClient redis.Client, interfaceName string) ([]prometheus.Metric, error) {
	var interfaceKey string = fmt.Sprintf("PORTCHANNEL|%s", interfaceName)
	collectedMetrics := make([]prometheus.Metric, 0) // Initialize an empty slice

	if strings.HasPrefix(interfaceName, "Ethernet") {
		interfaceKey = fmt.Sprintf("PORT|%s", interfaceName)
	}

	info, err := redisClient.HgetAllFromDb(ctx, "CONFIG_DB", interfaceKey)
	if err != nil {
		level.Error(logger).Log("msg", "Redis read failed", "err", err)
		return nil, err
	}

	description, ok := info["description"]
	if !ok {
		description = ""
	}

	mtu, err := strconv.ParseFloat(info["mtu"], 64)
	if err != nil {
		level.Error(logger).Log("msg", "Value parse failed", "err", err, "key", interfaceKey, "subKey", "mtu")
		return nil, err
	}

	speed, err := strconv.ParseFloat(info["speed"], 64)
	if err != nil {
		level.Error(logger).Log("msg", "Value parse failed", "err", err, "key", interfaceKey, "subKey", "speed")
		return nil, err
	}

	collectedMetrics = append(collectedMetrics, prometheus.MustNewConstMetric(
		collector.interfaceInfo, prometheus.GaugeValue, 1, interfaceName, info["alias"], info["index"], description,
	))

	collectedMetrics = append(collectedMetrics, prometheus.MustNewConstMetric(
		collector.interfaceMtu, prometheus.GaugeValue, mtu, interfaceName,
	))

	collectedMetrics = append(collectedMetrics, prometheus.MustNewConstMetric(
		collector.interfaceSpeed, prometheus.GaugeValue, speed*1000*1000/8, interfaceName,
	))

	return collectedMetrics, nil
}

func (collector *interfaceCollector) collectInterfaceByteCounters(interfaceName string, counters map[string]string) ([]prometheus.Metric, error) {
	var collectedMetrics []prometheus.Metric

	for _, direction := range []string{"in", "out"} {
		bytes, err := strconv.ParseFloat(counters[fmt.Sprintf(interfaceByteCountKey, strings.ToUpper(direction))], 64)
		if err != nil {
			level.Error(logger).Log("msg", "Value parse failed", "err", err)
			return nil, err
		}

		switch direction {
		case "in":
			collectedMetrics = append(collectedMetrics,
				prometheus.MustNewConstMetric(
					collector.interfaceReceivedBytes, prometheus.CounterValue, bytes, interfaceName,
				),
			)
		case "out":
			collectedMetrics = append(collectedMetrics,
				prometheus.MustNewConstMetric(
					collector.interfaceTransmitBytes, prometheus.CounterValue, bytes, interfaceName,
				),
			)
		}
	}

	return collectedMetrics, nil
}

func (collector *interfaceCollector) collectInterfaceErrCounters(interfaceName string, counters map[string]string) ([]prometheus.Metric, error) {
	var collectedMetrics []prometheus.Metric

	for _, direction := range []string{"in", "out"} {
		for errType, key := range interfaceErrorTypeMap[direction] {
			packets, err := strconv.ParseFloat(counters[key], 64)
			if err != nil {
				level.Error(logger).Log("msg", "Value parse failed", "err", err)
				return nil, err
			}

			switch direction {
			case "in":
				collectedMetrics = append(collectedMetrics,
					prometheus.MustNewConstMetric(
						collector.interfaceReceiveErrs, prometheus.CounterValue, packets, interfaceName, errType,
					),
				)
			case "out":
				collectedMetrics = append(collectedMetrics,
					prometheus.MustNewConstMetric(
						collector.interfaceTransmitErrs, prometheus.CounterValue, packets, interfaceName, errType,
					),
				)
			}
		}
	}

	return collectedMetrics, nil
}

func (collector *interfaceCollector) collectInterfacePacketCounters(interfaceName string, counters map[string]string) ([]prometheus.Metric, error) {
	var collectedMetrics []prometheus.Metric

	for _, direction := range []string{"in", "out"} {
		for _, method := range []string{"ucast", "broadcast", "multicast"} {
			packets, err := strconv.ParseFloat(counters[fmt.Sprintf(interfacePacketCountKey, strings.ToUpper(direction), strings.ToUpper(method))], 64)
			if err != nil {
				level.Error(logger).Log("msg", "Value parse failed", "err", err)
				return nil, err
			}

			switch direction {
			case "in":
				collectedMetrics = append(collectedMetrics,
					prometheus.MustNewConstMetric(
						collector.interfaceReceivePackets, prometheus.CounterValue, packets, interfaceName, method,
					),
				)
			case "out":
				collectedMetrics = append(collectedMetrics,
					prometheus.MustNewConstMetric(
						collector.interfaceTransmitPackets, prometheus.CounterValue, packets, interfaceName, method,
					),
				)
			}
		}
	}

	return collectedMetrics, nil
}

func (collector *interfaceCollector) collectInterfacePacketSizeCounters(interfaceName string, counters map[string]string) ([]prometheus.Metric, error) {
	var (
		collectedMetrics []prometheus.Metric
		size, key        string
	)

	for _, direction := range []string{"in", "out"} {
		for size, key = range interfacePacketSizeKeyMap {
			bytes, err := strconv.ParseFloat(counters[fmt.Sprintf(key, strings.ToUpper(direction))], 64)
			if err != nil {
				level.Error(logger).Log("msg", "Value parse failed", "err", err)
				return nil, err
			}

			switch direction {
			case "in":
				collectedMetrics = append(collectedMetrics, prometheus.MustNewConstMetric(
					collector.interfaceReceiveEthernetPackets, prometheus.CounterValue, bytes, interfaceName, size,
				))
			case "out":
				collectedMetrics = append(collectedMetrics, prometheus.MustNewConstMetric(
					collector.interfaceTransmitEthernetPackets, prometheus.CounterValue, bytes, interfaceName, size,
				))
			}
		}
	}

	return collectedMetrics, nil
}
