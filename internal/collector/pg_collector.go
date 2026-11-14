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

var pgInclude = kingpin.Flag("collector.pg.include-pgs",
	"Priority groups exported by the pg collector: a list such as 3,4 or 0-7, or 'all'. Default: the lossless PGs.").
	Default("3,4").String()

var pgTypes = kingpin.Flag("collector.pg.types",
	"Watermark types exported by the pg collector: any of user, persistent, periodic, comma separated. 'periodic' alone keeps all eight PGs cheap.").
	Default("user,persistent,periodic").String()

// pgTypeSet returns the upper case watermark table prefixes to read.
func pgTypeSet(spec string) []string {
	var out []string

	for _, t := range strings.Split(spec, ",") {
		t = strings.ToUpper(strings.TrimSpace(t))
		if t == "USER" || t == "PERSISTENT" || t == "PERIODIC" {
			out = append(out, t)
		}
	}

	if len(out) == 0 {
		return watermarkTypes
	}

	return out
}

// pgFilter returns the set of PG indexes to export; nil means all.
func pgFilter(spec string) map[string]bool {
	if strings.TrimSpace(spec) == "all" {
		return nil
	}

	set := map[string]bool{}
	for _, n := range parseIntList(spec) {
		set[strconv.Itoa(n)] = true
	}

	return set
}

// NewPgCollector exports ingress priority group watermarks (shared and XOFF headroom) and, when the
// switch has the PG_DROP flex counter group, PG drops.
func NewPgCollector(logger log.Logger) *cachedCollector {
	watermark := prometheus.NewDesc("sonic_pg_watermark_bytes",
		"Ingress priority group watermark in bytes (watermark: shared or headroom, type: user, persistent or periodic)",
		[]string{"device", "pg", "watermark", "type"}, nil)
	dropped := prometheus.NewDesc("sonic_pg_dropped_packets_total",
		"Packets dropped by an ingress priority group (needs the PG_DROP flex counter group)", []string{"device", "pg"}, nil)

	scrape := func(ctx context.Context, rc redis.Client) ([]prometheus.Metric, error) {
		pgs, err := rc.HgetAllFromDb(ctx, "COUNTERS_DB", "COUNTERS_PG_NAME_MAP")
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		include := pgFilter(*pgInclude)
		types := pgTypeSet(*pgTypes)
		keys := make([]string, 0, len(pgs)*4)

		for name, oid := range pgs {
			if _, index, ok := splitDeviceIndex(name); ok && (include == nil || include[index]) {
				keys = append(keys, "COUNTERS:"+oid)
				for _, t := range types {
					keys = append(keys, t+"_WATERMARKS:"+oid)
				}
			}
		}

		hashes, err := rc.PipelineHGetAll(ctx, "COUNTERS_DB", keys)
		if err != nil {
			return nil, fmt.Errorf("redis read failed: %w", err)
		}

		var metrics []prometheus.Metric

		for name, oid := range pgs {
			device, index, ok := splitDeviceIndex(name)
			if !ok || (include != nil && !include[index]) {
				continue
			}

			if raw, present := hashes["COUNTERS:"+oid]["SAI_INGRESS_PRIORITY_GROUP_STAT_DROPPED_PACKETS"]; present {
				if value, ok := parseValue("pg", raw); ok {
					metrics = append(metrics, prometheus.MustNewConstMetric(dropped, prometheus.CounterValue, value, device, index))
				}
			}

			for _, t := range types {
				for field, label := range map[string]string{
					"SAI_INGRESS_PRIORITY_GROUP_STAT_SHARED_WATERMARK_BYTES":    "shared",
					"SAI_INGRESS_PRIORITY_GROUP_STAT_XOFF_ROOM_WATERMARK_BYTES": "headroom",
				} {
					raw, present := hashes[t+"_WATERMARKS:"+oid][field]
					if !present {
						continue
					}

					if value, ok := parseValue("pg", raw); ok {
						metrics = append(metrics, prometheus.MustNewConstMetric(watermark, prometheus.GaugeValue, value,
							device, index, label, strings.ToLower(t)))
					}
				}
			}
		}

		return metrics, nil
	}

	return newCachedCollector("pg", logger, scrape, watermark, dropped)
}
