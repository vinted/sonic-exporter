package collector

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

var qosMaps = []string{"dscp_to_tc_map", "dot1p_to_tc_map", "tc_to_queue_map", "tc_to_pg_map", "pfc_to_queue_map"}

// configKeys scans CONFIG_DB for the given patterns and reads every match in pipelines.
func configKeys(ctx context.Context, rc redis.Client, patterns ...string) (map[string]map[string]string, error) {
	var keys []string

	for _, pattern := range patterns {
		found, err := rc.ScanKeys(ctx, "CONFIG_DB", pattern)
		if err != nil {
			return nil, fmt.Errorf("redis scan failed: %w", err)
		}

		keys = append(keys, found...)
	}

	hashes, err := rc.PipelineHGetAll(ctx, "CONFIG_DB", keys)
	if err != nil {
		return nil, fmt.Errorf("redis read failed: %w", err)
	}

	return hashes, nil
}

// NewQosConfigCollector exports what is configured for lossless operation (CONFIG_DB only, not what the
// ASIC applied): PFC-enabled priorities, QoS map bindings, buffer profiles per PG and queue, WRED/ECN
// profiles and the PFC watchdog. Compliance rules compare these series, so a missing egress lossless
// profile is visible before any traffic.
func NewQosConfigCollector(logger log.Logger) *cachedCollector {
	pfcEnabled := prometheus.NewDesc("sonic_port_pfc_enabled", "1 when PFC is enabled for the priority on the port (PORT_QOS_MAP pfc_enable)", []string{"device", "priority"}, nil)
	mapBound := prometheus.NewDesc("sonic_port_qos_map_bound", "1 when the QoS map is bound to the port (PORT_QOS_MAP)", []string{"device", "map"}, nil)
	pgProfile := prometheus.NewDesc("sonic_buffer_pg_profile_info", "Buffer profile bound to the ingress priority groups of a port (BUFFER_PG), value 1", []string{"device", "pgs", "profile"}, nil)
	queueProfile := prometheus.NewDesc("sonic_buffer_queue_profile_info", "Buffer profile bound to the egress queues of a port (BUFFER_QUEUE), value 1", []string{"device", "queues", "profile"}, nil)
	wredBound := prometheus.NewDesc("sonic_queue_wred_profile_bound", "WRED/ECN profile bound to the queues of a port (QUEUE), value 1", []string{"device", "queues", "profile"}, nil)
	wredEcn := prometheus.NewDesc("sonic_wred_ecn_enabled", "1 when the WRED profile marks ECN (WRED_PROFILE ecn is not ecn_none)", []string{"profile"}, nil)
	pfcwd := prometheus.NewDesc("sonic_pfcwd_configured", "1 when a PFC watchdog is configured on the port (PFC_WD)", []string{"device"}, nil)
	pfcwdDefault := prometheus.NewDesc("sonic_pfcwd_default_enabled", "1 when DEVICE_METADATA default_pfcwd_status is enable", nil, nil)

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		hashes, err := configKeys(ctx, rc, "PORT_QOS_MAP|*", "BUFFER_PG|*", "BUFFER_QUEUE|*", "QUEUE|*", "WRED_PROFILE|*", "PFC_WD|*", "DEVICE_METADATA|localhost")
		if err != nil {
			return nil, err
		}

		var metrics []prometheus.Metric

		for key, hash := range hashes {
			table, rest, _ := strings.Cut(key, "|")
			device, sub, _ := strings.Cut(rest, "|")

			switch table {
			case "PORT_QOS_MAP":
				enabled := map[int]bool{}
				for _, p := range parseIntList(hash["pfc_enable"]) {
					enabled[p] = true
				}

				for priority := 0; priority < 8; priority++ {
					value := 0.0
					if enabled[priority] {
						value = 1
					}

					metrics = append(metrics, prometheus.MustNewConstMetric(pfcEnabled, prometheus.GaugeValue, value, rest, strconv.Itoa(priority)))
				}

				for _, name := range qosMaps {
					value := 0.0
					if hash[name] != "" {
						value = 1
					}

					metrics = append(metrics, prometheus.MustNewConstMetric(mapBound, prometheus.GaugeValue, value, rest, name))
				}
			case "BUFFER_PG":
				metrics = append(metrics, prometheus.MustNewConstMetric(pgProfile, prometheus.GaugeValue, 1, device, sub, stripBrackets(hash["profile"])))
			case "BUFFER_QUEUE":
				metrics = append(metrics, prometheus.MustNewConstMetric(queueProfile, prometheus.GaugeValue, 1, device, sub, stripBrackets(hash["profile"])))
			case "QUEUE":
				if hash["wred_profile"] != "" {
					metrics = append(metrics, prometheus.MustNewConstMetric(wredBound, prometheus.GaugeValue, 1, device, sub, stripBrackets(hash["wred_profile"])))
				}
			case "WRED_PROFILE":
				value := 0.0
				if ecn := hash["ecn"]; ecn != "" && ecn != "ecn_none" {
					value = 1
				}

				metrics = append(metrics, prometheus.MustNewConstMetric(wredEcn, prometheus.GaugeValue, value, rest))
			case "PFC_WD":
				if rest != "GLOBAL" {
					metrics = append(metrics, prometheus.MustNewConstMetric(pfcwd, prometheus.GaugeValue, 1, rest))
				}
			case "DEVICE_METADATA":
				value := 0.0
				if hash["default_pfcwd_status"] == "enable" {
					value = 1
				}

				metrics = append(metrics, prometheus.MustNewConstMetric(pfcwdDefault, prometheus.GaugeValue, value))
			}
		}

		return metrics, nil
	}

	return newCachedCollector("qos_config", logger, scrape, pfcEnabled, mapBound, pgProfile, queueProfile, wredBound, wredEcn, pfcwd, pfcwdDefault)
}
