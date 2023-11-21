package collector

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

type packetSize string

type interfaceCollector struct {
	interfaceInfo                    *prometheus.Desc
	interfaceMtu                     *prometheus.Desc
	interfaceSpeed                   *prometheus.Desc
	interfaceAdminStatus             *prometheus.Desc
	interfaceOperationslStatus       *prometheus.Desc
	interfaceTransceiverTemperature  *prometheus.Desc
	interfaceTransceiverVoltage      *prometheus.Desc
	interfaceOpticTransmitPower      *prometheus.Desc
	interfaceTransmitEthernetPackets *prometheus.Desc
	interfaceTransmitPackets         *prometheus.Desc
	interfaceTransmitBytes           *prometheus.Desc
	interfaceTransmitErrs            *prometheus.Desc
	interfaceOpticReceivePower       *prometheus.Desc
	interfaceReceiveEthernetPackets  *prometheus.Desc
	interfaceReceivePackets          *prometheus.Desc
	interfaceReceivedBytes           *prometheus.Desc
	interfaceReceiveErrs             *prometheus.Desc
	scrapeDuration                   *prometheus.Desc
	scrapeCollectorSuccess           *prometheus.Desc
	cachedMetrics                    []prometheus.Metric
	lastScrapeTime                   time.Time
	logger                           log.Logger
	mu                               sync.Mutex
}

func NewInterfaceCollector(logger log.Logger) *interfaceCollector {
	const (
		namespace = "sonic"
		subsystem = "interface"
	)

	return &interfaceCollector{
		interfaceInfo: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "info"),
			"Non-numeric data about interface, value is always 1", []string{"device", "alias", "index", "description"}, nil),
		interfaceMtu: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "mtu_bytes"),
			"Network device property: mtu_bytes", []string{"device"}, nil),
		interfaceSpeed: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "speed_bytes"),
			"Network device property: speed_bytes", []string{"device"}, nil),
		interfaceAdminStatus: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "admin_status"),
			"Network device administrative status: 0(DOWN), 1(UP)", []string{"device"}, nil),
		interfaceOperationslStatus: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "operational_status"),
			"Network device operational status:  0(DOWN), 1(UP)", []string{"device"}, nil),
		interfaceTransceiverTemperature: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transceiver_temperature_celsius"),
			"Network device transceiver temperature (celsius)", []string{"device"}, nil),
		interfaceTransceiverVoltage: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transceiver_voltage"),
			"Network device transceiver voltage", []string{"device"}, nil),
		interfaceOpticTransmitPower: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "optic_transmit_power_dbm"),
			"Network device transceiver voltage", []string{"device", "unit"}, nil),
		interfaceTransmitEthernetPackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transmit_ethernet_packets_total"),
			"Number of ethernet packets transmitted on an interface", []string{"device", "size"}, nil),
		interfaceTransmitPackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transmit_packets_total"),
			"Number of packets transmitted on an interface", []string{"device", "method"}, nil),
		interfaceTransmitErrs: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transmit_errs_total"),
			"Number of transmit errs on an interface", []string{"device", "type"}, nil),
		interfaceTransmitBytes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "transmit_bytes_total"),
			"Number of bytes transmitted on an interface", []string{"device"}, nil),
		interfaceOpticReceivePower: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "optic_receive_power_dbm"),
			"Network device transceiver voltage", []string{"device", "unit"}, nil),
		interfaceReceiveEthernetPackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "receive_ethernet_packets_total"),
			"Number of ethernet packets received on an interface", []string{"device", "size"}, nil),
		interfaceReceivePackets: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "receive_packets_total"),
			"Number of packets received on an interface", []string{"device", "method"}, nil),
		interfaceReceiveErrs: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "receive_errs_total"),
			"Number of receive errs on an interface", []string{"device", "type"}, nil),
		interfaceReceivedBytes: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "receive_bytes_total"),
			"Number of bytes received on an interface", []string{"device"}, nil),
		scrapeDuration: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "scrape_duration_seconds"),
			"Time it took for prometheus to scrape sonic interface metrics", nil, nil),
		scrapeCollectorSuccess: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, "collector_success"),
			"Whether interface collector succeeded", nil, nil),
		logger: logger,
	}
}

func (collector *interfaceCollector) Collect(ch chan<- prometheus.Metric) {
	const cacheDuration = 15 * time.Second

	scrapeSuccess := 1.0

	var ctx = context.Background()

	collector.mu.Lock()
	defer collector.mu.Unlock()

	if time.Since(collector.lastScrapeTime) < cacheDuration {
		// Return cached metrics without making redis calls
		level.Info(collector.logger).Log("msg", "Returning interface metrics from cache")

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

func (collector *interfaceCollector) scrapeMetrics(ctx context.Context) error {
	level.Info(collector.logger).Log("msg", "Starting interface metric scrape")
	scrapeTime := time.Now()

	redisClient, err := redis.NewClient()
	if err != nil {
		return fmt.Errorf("redis client initialization failed: %w", err)
	}

	defer redisClient.Close()

	// Reset metrics
	collector.cachedMetrics = []prometheus.Metric{}

	ports, err := redisClient.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_PORT_NAME_MAP")
	if err != nil {
		return fmt.Errorf("redis read failed: %w", err)
	}

	for port := range ports {
		counterKey := fmt.Sprintf("COUNTERS:%s", ports[port])

		err := collector.collectInterfaceCounters(ctx, redisClient, port, counterKey)
		if err != nil {
			return fmt.Errorf("interface counters collection failed: %w", err)
		}

		err = collector.collectInterfaceInfo(ctx, redisClient, port)
		if err != nil {
			return fmt.Errorf("interface info collection failed: %w", err)
		}

	}

	err = collector.collectInterfaceOpticalInfo(ctx, redisClient)
	if err != nil {
		return fmt.Errorf("interface optical info collection failed: %w", err)
	}

	level.Info(collector.logger).Log("msg", "Ending interface metric scrape")

	collector.lastScrapeTime = time.Now()
	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.scrapeDuration, prometheus.GaugeValue, time.Since(scrapeTime).Seconds(),
	))
	return nil
}

func (collector *interfaceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.interfaceInfo
	ch <- collector.interfaceMtu
	ch <- collector.interfaceSpeed
	ch <- collector.interfaceAdminStatus
	ch <- collector.interfaceOperationslStatus
	ch <- collector.interfaceTransceiverTemperature
	ch <- collector.interfaceTransceiverVoltage
	ch <- collector.interfaceOpticTransmitPower
	ch <- collector.interfaceTransmitEthernetPackets
	ch <- collector.interfaceTransmitPackets
	ch <- collector.interfaceTransmitErrs
	ch <- collector.interfaceTransmitBytes
	ch <- collector.interfaceOpticReceivePower
	ch <- collector.interfaceReceiveEthernetPackets
	ch <- collector.interfaceReceivePackets
	ch <- collector.interfaceReceiveErrs
	ch <- collector.interfaceReceivedBytes
	ch <- collector.scrapeDuration
	ch <- collector.scrapeCollectorSuccess
}

func (collector *interfaceCollector) collectInterfaceCounters(ctx context.Context, redisClient redis.Client, interfaceName, counterKey string) error {
	var counters map[string]string

	// Retrieve packet counters from redis database
	counters, err := redisClient.HgetAllFromDb(ctx, "COUNTERS_DB", counterKey)
	if err != nil {
		return fmt.Errorf("redis read failed: %w", err)
	}

	err = collector.collectInterfaceByteCounters(interfaceName, counters)
	if err != nil {
		return fmt.Errorf("byte counters collection failed: %w", err)
	}

	err = collector.collectInterfaceErrCounters(interfaceName, counters)
	if err != nil {
		return fmt.Errorf("err counters collection failed: %w", err)
	}

	err = collector.collectInterfacePacketCounters(interfaceName, counters)
	if err != nil {
		return fmt.Errorf("packet counters collection failed: %w", err)
	}

	err = collector.collectInterfacePacketSizeCounters(interfaceName, counters)
	if err != nil {
		return fmt.Errorf("packet size counters collection failed: %w", err)
	}

	return nil

}

func (collector *interfaceCollector) collectInterfaceInfo(ctx context.Context, redisClient redis.Client, interfaceName string) error {
	err := collector.collectInterfaceConfigInfo(ctx, redisClient, interfaceName)
	if err != nil {
		return err
	}

	err = collector.collectInterfaceOperationInfo(ctx, redisClient, interfaceName)
	if err != nil {
		return err
	}

	return nil
}

func (collector *interfaceCollector) collectInterfaceConfigInfo(ctx context.Context, redisClient redis.Client, interfaceName string) error {
	var interfaceKey string = fmt.Sprintf("PORTCHANNEL|%s", interfaceName)

	if strings.HasPrefix(interfaceName, "Ethernet") {
		interfaceKey = fmt.Sprintf("PORT|%s", interfaceName)
	}

	info, err := redisClient.HgetAllFromDb(ctx, "CONFIG_DB", interfaceKey)
	if err != nil {
		return fmt.Errorf("redis read failed: %w", err)
	}

	description, ok := info["description"]
	if !ok {
		description = ""
	}

	mtu, err := parseFloat(info["mtu"])
	if err != nil {
		return fmt.Errorf("value parse failed: %w", err)
	}

	speed, err := parseFloat(info["speed"])
	if err != nil {
		return fmt.Errorf("value parse failed: %w", err)
	}

	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.interfaceInfo, prometheus.GaugeValue, 1, interfaceName, info["alias"], info["index"], description,
	))

	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.interfaceMtu, prometheus.GaugeValue, mtu, interfaceName,
	))

	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.interfaceSpeed, prometheus.GaugeValue, speed*1000*1000/8, interfaceName,
	))

	return nil
}

func (collector *interfaceCollector) collectInterfaceOperationInfo(ctx context.Context, redisClient redis.Client, interfaceName string) error {
	var (
		portKey           string  = fmt.Sprintf("PORT_TABLE:%s", interfaceName)
		adminStatus       float64 = 0
		operationalStatus float64 = 0
	)

	info, err := redisClient.HgetAllFromDb(ctx, "APPL_DB", portKey)
	if err != nil {
		return fmt.Errorf("redis read failed: %w", err)
	}

	if info["admin_status"] == "up" {
		adminStatus = 1
	}

	if info["oper_status"] == "up" {
		operationalStatus = 1
	}

	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.interfaceAdminStatus, prometheus.GaugeValue, adminStatus, interfaceName,
	))

	collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
		collector.interfaceOperationslStatus, prometheus.GaugeValue, operationalStatus, interfaceName,
	))

	return nil
}

func (collector *interfaceCollector) collectInterfaceOpticalInfo(ctx context.Context, redisClient redis.Client) error {
	const transceiverKeyPattern string = "TRANSCEIVER_DOM_SENSOR|*"
	var (
		rxPowerRegex = regexp.MustCompile(`^rx(\d*)power`)
		txPowerRegex = regexp.MustCompile(`^tx(\d*)power`)
	)

	transceiverKeys, err := redisClient.KeysFromDb(ctx, "APPL_DB", transceiverKeyPattern)
	if err != nil {
		return err
	}

	for _, transceiverKey := range transceiverKeys {
		interfaceName := strings.Split(transceiverKey, "|")[1]

		data, err := redisClient.HgetAllFromDb(ctx, "APPL_DB", transceiverKey)
		if err != nil {
			return err
		}

		for metric, value := range data {
			parsedValue, err := parseFloat(value)
			if err != nil {
				continue
			}

			switch name := metric; {
			case name == "temperature":
				collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
					collector.interfaceTransceiverTemperature, prometheus.GaugeValue, parsedValue, interfaceName,
				))
			case name == "voltage":
				collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
					collector.interfaceTransceiverVoltage, prometheus.GaugeValue, parsedValue, interfaceName,
				))
			case rxPowerRegex.MatchString(name):
				opticUnit := rxPowerRegex.FindStringSubmatch(name)[1]
				collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
					collector.interfaceOpticReceivePower, prometheus.GaugeValue, parsedValue, interfaceName, opticUnit,
				))
			case txPowerRegex.MatchString(name):
				opticUnit := txPowerRegex.FindStringSubmatch(name)[1]
				collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
					collector.interfaceOpticTransmitPower, prometheus.GaugeValue, parsedValue, interfaceName, opticUnit,
				))
			}
		}
	}
	return nil
}

func (collector *interfaceCollector) collectInterfaceByteCounters(interfaceName string, counters map[string]string) error {
	const interfaceByteCountKey = "SAI_PORT_STAT_IF_%s_OCTETS"

	for _, direction := range []string{"in", "out"} {
		bytes, err := parseFloat(counters[fmt.Sprintf(interfaceByteCountKey, strings.ToUpper(direction))])
		if err != nil {
			return fmt.Errorf("value parse failed: %w", err)
		}

		switch direction {
		case "in":
			collector.cachedMetrics = append(collector.cachedMetrics,
				prometheus.MustNewConstMetric(
					collector.interfaceReceivedBytes, prometheus.CounterValue, bytes, interfaceName,
				),
			)
		case "out":
			collector.cachedMetrics = append(collector.cachedMetrics,
				prometheus.MustNewConstMetric(
					collector.interfaceTransmitBytes, prometheus.CounterValue, bytes, interfaceName,
				),
			)
		}
	}

	return nil
}

func (collector *interfaceCollector) collectInterfaceErrCounters(interfaceName string, counters map[string]string) error {
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

	for _, direction := range []string{"in", "out"} {
		for errType, key := range interfaceErrorTypeMap[direction] {
			packets, err := parseFloat(counters[key])
			if err != nil {
				return fmt.Errorf("value parse failed: %w", err)
			}

			switch direction {
			case "in":
				collector.cachedMetrics = append(collector.cachedMetrics,
					prometheus.MustNewConstMetric(
						collector.interfaceReceiveErrs, prometheus.CounterValue, packets, interfaceName, errType,
					),
				)
			case "out":
				collector.cachedMetrics = append(collector.cachedMetrics,
					prometheus.MustNewConstMetric(
						collector.interfaceTransmitErrs, prometheus.CounterValue, packets, interfaceName, errType,
					),
				)
			}
		}
	}

	return nil
}

func (collector *interfaceCollector) collectInterfacePacketCounters(interfaceName string, counters map[string]string) error {
	const interfacePacketCountKey = "SAI_PORT_STAT_IF_%s_%s_PKTS"

	for _, direction := range []string{"in", "out"} {
		for _, method := range []string{"ucast", "broadcast", "multicast"} {
			packets, err := parseFloat(counters[fmt.Sprintf(interfacePacketCountKey, strings.ToUpper(direction), strings.ToUpper(method))])
			if err != nil {
				return fmt.Errorf("value parse failed: %w", err)
			}

			switch direction {
			case "in":
				collector.cachedMetrics = append(collector.cachedMetrics,
					prometheus.MustNewConstMetric(
						collector.interfaceReceivePackets, prometheus.CounterValue, packets, interfaceName, method,
					),
				)
			case "out":
				collector.cachedMetrics = append(collector.cachedMetrics,
					prometheus.MustNewConstMetric(
						collector.interfaceTransmitPackets, prometheus.CounterValue, packets, interfaceName, method,
					),
				)
			}
		}
	}

	return nil
}

func (p packetSize) format(direction string) string {
	direction = strings.ToUpper(direction)

	switch p {
	case "64":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_64_OCTETS", direction)
	case "127":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_65_TO_127_OCTETS", direction)
	case "255":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_128_TO_255_OCTETS", direction)
	case "511":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_256_TO_511_OCTETS", direction)
	case "1023":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_512_TO_1023_OCTETS", direction)
	case "1518":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_1024_TO_1518_OCTETS", direction)
	case "2047":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_1519_TO_2047_OCTETS", direction)
	case "4095":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_2048_TO_4095_OCTETS", direction)
	case "9216":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_4096_TO_9216_OCTETS", direction)
	case "16383":
		return fmt.Sprintf("SAI_PORT_STAT_ETHER_%s_PKTS_9217_TO_16383_OCTETS", direction)
	}

	return ""
}

func (collector *interfaceCollector) collectInterfacePacketSizeCounters(interfaceName string, counters map[string]string) error {
	var sizes = []packetSize{"64", "127", "255", "511", "1023", "1518", "2047", "4095", "9216", "16383"}

	for _, direction := range []string{"in", "out"} {
		for _, size := range sizes {
			bytes, err := parseFloat(counters[size.format(direction)])
			if err != nil {
				return fmt.Errorf("value parse failed: %w", err)
			}

			switch direction {
			case "in":
				collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
					collector.interfaceReceiveEthernetPackets, prometheus.CounterValue, bytes, interfaceName, string(size),
				))
			case "out":
				collector.cachedMetrics = append(collector.cachedMetrics, prometheus.MustNewConstMetric(
					collector.interfaceTransmitEthernetPackets, prometheus.CounterValue, bytes, interfaceName, string(size),
				))
			}
		}
	}

	return nil
}
