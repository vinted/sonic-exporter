package collector

import (
	"context"
	"fmt"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

// NewEcnPfcwdCollector exports ECN marks, WRED drops and PFC watchdog counters from the queue counters.
// Every series is emitted only when the field exists, so on a switch without the WRED stats or without
// pfcwd started the collector exports nothing but its success and duration metrics.
func NewEcnPfcwdCollector(logger log.Logger) *cachedCollector {
	ecnMarked := prometheus.NewDesc("sonic_queue_ecn_marked_packets_total", "Packets marked with ECN by WRED in a queue", []string{"device", "queue"}, nil)
	wredDropped := prometheus.NewDesc("sonic_queue_wred_dropped_packets_total", "Packets dropped by WRED in a queue", []string{"device", "queue"}, nil)
	stormed := prometheus.NewDesc("sonic_pfcwd_queue_stormed", "1 when the PFC watchdog reports the queue as stormed (PFC_WD_STATUS)", []string{"device", "queue"}, nil)
	detected := prometheus.NewDesc("sonic_pfcwd_storm_detected_total", "PFC storms detected by the watchdog on a queue", []string{"device", "queue"}, nil)
	restored := prometheus.NewDesc("sonic_pfcwd_storm_restored_total", "PFC storms restored by the watchdog on a queue", []string{"device", "queue"}, nil)
	dropped := prometheus.NewDesc("sonic_pfcwd_dropped_packets_total", "Packets dropped by the PFC watchdog action", []string{"device", "queue", "direction"}, nil)

	counterFields := []struct {
		field string
		desc  *prometheus.Desc
		extra string
	}{
		{"SAI_QUEUE_STAT_WRED_ECN_MARKED_PACKETS", ecnMarked, ""},
		{"SAI_QUEUE_STAT_WRED_DROPPED_PACKETS", wredDropped, ""},
		{"PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED", detected, ""},
		{"PFC_WD_QUEUE_STATS_DEADLOCK_RESTORED", restored, ""},
		{"PFC_WD_QUEUE_STATS_TX_DROPPED_PACKETS", dropped, "transmit"},
		{"PFC_WD_QUEUE_STATS_RX_DROPPED_PACKETS", dropped, "receive"},
	}

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		names, err := rc.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_QUEUE_NAME_MAP")
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		keys := make([]string, 0, len(names))
		for _, oid := range names {
			keys = append(keys, "COUNTERS:"+oid)
		}

		hashes, err := rc.PipelineHGetAll(ctx, "COUNTERS_DB", keys)
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		var metrics []prometheus.Metric

		for name, oid := range names {
			device, queue, ok := splitDeviceIndex(name)
			if !ok {
				recordError("ecn_pfcwd", "parse")
				continue
			}

			hash := hashes["COUNTERS:"+oid]

			for _, item := range counterFields {
				raw, present := hash[item.field]
				if !present {
					continue
				}

				value, ok := parseValue("ecn_pfcwd", raw)
				if !ok {
					continue
				}

				labels := []string{device, queue}
				if item.extra != "" {
					labels = append(labels, item.extra)
				}

				metrics = append(metrics, prometheus.MustNewConstMetric(item.desc, prometheus.CounterValue, value, labels...))
			}

			if status, present := hash["PFC_WD_STATUS"]; present {
				value := 0.0
				if status == "stormed" {
					value = 1
				}

				metrics = append(metrics, prometheus.MustNewConstMetric(stormed, prometheus.GaugeValue, value, device, queue))
			}
		}

		return metrics, nil
	}

	return newCachedCollector("ecn_pfcwd", logger, scrape, ecnMarked, wredDropped, stormed, detected, restored, dropped)
}
