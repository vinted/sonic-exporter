package collector

import (
	"fmt"

	"github.com/alecthomas/kingpin/v2"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
)

type factory func(logger log.Logger) prometheus.Collector

// Version is set at build time with -ldflags "-X github.com/vinted/sonic-exporter/internal/collector.Version=...".
var (
	Version  = "dev"
	Revision = "unknown"
)

var collectorNames = []string{"interface", "hw", "crm", "queue", "pfc", "pg", "buffer_pool", "queue_info", "qos_config", "ecn_pfcwd", "lldp", "queue_limit", "qos_map"}

// defaultEnabled lists the collectors that are on by default: the four original ones. The collectors
// added for lossless-fabric visibility are opt-in with --collector.<name> until validated on a rack.
var defaultEnabled = map[string]bool{"interface": true, "hw": true, "crm": true, "queue": true}

var factories = map[string]factory{
	"interface": func(logger log.Logger) prometheus.Collector { return NewInterfaceCollector(logger) },
	"hw":        func(logger log.Logger) prometheus.Collector { return NewHwCollector(logger) },
	"crm":       func(logger log.Logger) prometheus.Collector { return NewCrmCollector(logger) },
	"queue":     func(logger log.Logger) prometheus.Collector { return NewQueueCollector(logger) },

	"pfc":         func(logger log.Logger) prometheus.Collector { return NewPfcCollector(logger) },
	"pg":          func(logger log.Logger) prometheus.Collector { return NewPgCollector(logger) },
	"buffer_pool": func(logger log.Logger) prometheus.Collector { return NewBufferPoolCollector(logger) },
	"queue_info":  func(logger log.Logger) prometheus.Collector { return NewQueueInfoCollector(logger) },
	"qos_config":  func(logger log.Logger) prometheus.Collector { return NewQosConfigCollector(logger) },
	"ecn_pfcwd":   func(logger log.Logger) prometheus.Collector { return NewEcnPfcwdCollector(logger) },
	"lldp":        func(logger log.Logger) prometheus.Collector { return NewLldpCollector(logger) },

	"queue_limit": func(logger log.Logger) prometheus.Collector { return NewQueueLimitCollector(logger) },
	"qos_map":     func(logger log.Logger) prometheus.Collector { return NewQosMapCollector(logger) },
}

var collectorState = registerCollectorFlags()

func registerCollectorFlags() map[string]*bool {
	state := make(map[string]*bool, len(collectorNames))

	for _, name := range collectorNames {
		def := "false"
		if defaultEnabled[name] {
			def = "true"
		}

		state[name] = kingpin.Flag(
			fmt.Sprintf("collector.%s", name),
			fmt.Sprintf("Enable the %s collector (default %s).", name, def),
		).Default(def).Bool()
	}

	return state
}

func Register(reg prometheus.Registerer, logger log.Logger) error {
	enabled := 0

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sonic_exporter_build_info",
		Help: "Exporter version and revision, value is always 1.",
	}, []string{"version", "revision"})
	buildInfo.WithLabelValues(Version, Revision).Set(1)

	for _, c := range []prometheus.Collector{scrapeErrors, buildInfo} {
		if err := reg.Register(c); err != nil {
			return fmt.Errorf("failed to register exporter metrics: %w", err)
		}
	}

	for _, name := range collectorNames {
		if !*collectorState[name] {
			level.Info(logger).Log("msg", "Collector disabled", "collector", name)
			continue
		}

		if err := reg.Register(factories[name](logger)); err != nil {
			return fmt.Errorf("failed to register %s collector: %w", name, err)
		}

		level.Debug(logger).Log("msg", "Collector enabled", "collector", name)
		enabled++
	}

	if enabled == 0 {
		level.Warn(logger).Log("msg", "All collectors are disabled, no sonic metrics will be exposed")
	}

	return nil
}
