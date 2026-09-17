package collector

import (
	"fmt"

	"github.com/alecthomas/kingpin/v2"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
)

type factory func(logger log.Logger) prometheus.Collector

var collectorNames = []string{"interface", "hw", "crm", "queue"}

var factories = map[string]factory{
	"interface": func(logger log.Logger) prometheus.Collector { return NewInterfaceCollector(logger) },
	"hw":        func(logger log.Logger) prometheus.Collector { return NewHwCollector(logger) },
	"crm":       func(logger log.Logger) prometheus.Collector { return NewCrmCollector(logger) },
	"queue":     func(logger log.Logger) prometheus.Collector { return NewQueueCollector(logger) },
}

var collectorState = registerCollectorFlags()

func registerCollectorFlags() map[string]*bool {
	state := make(map[string]*bool, len(collectorNames))

	for _, name := range collectorNames {
		state[name] = kingpin.Flag(
			fmt.Sprintf("collector.%s", name),
			fmt.Sprintf("Enable the %s collector.", name),
		).Default("true").Bool()
	}

	return state
}

func Register(reg prometheus.Registerer, logger log.Logger) error {
	enabled := 0

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
