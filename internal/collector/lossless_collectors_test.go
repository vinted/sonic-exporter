package collector

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

// Fixtures in fixtures/rack were captured read-only from a rack switch (AS7816-64X, SONiC 202006)
var rackFixtures = []string{
	"../../fixtures/rack/counters_db_data.json",
	"../../fixtures/rack/config_db_data.json",
	"../../fixtures/rack/appl_db_data.json",
	"../../fixtures/rack/state_db_data.json",
}

// rackRedis starts an isolated miniredis seeded with the rack fixtures and points the exporter at it.
func rackRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}

	previous, had := os.LookupEnv("REDIS_ADDRESS")
	os.Setenv("REDIS_ADDRESS", server.Addr())

	t.Cleanup(func() {
		server.Close()

		if had {
			os.Setenv("REDIS_ADDRESS", previous)
		} else {
			os.Unsetenv("REDIS_ADDRESS")
		}
	})

	for _, file := range rackFixtures {
		if err := pushDataFromFile(context.Background(), file); err != nil {
			t.Fatalf("load %s: %v", file, err)
		}
	}

	return server
}

func rackCounters(t *testing.T) map[string]map[string]string {
	t.Helper()

	raw, err := os.ReadFile("../../fixtures/rack/counters_db_data.json")
	if err != nil {
		t.Fatal(err)
	}

	var db redisDatabase
	if err := json.Unmarshal(raw, &db); err != nil {
		t.Fatal(err)
	}

	return db.Data
}

func gather(t *testing.T, c prometheus.Collector) map[string]*dto.MetricFamily {
	t.Helper()

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(c)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	byName := map[string]*dto.MetricFamily{}
	for _, family := range families {
		byName[family.GetName()] = family
	}

	return byName
}

func value(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) (float64, bool) {
	t.Helper()

	family, ok := families[name]
	if !ok {
		return 0, false
	}

	for _, metric := range family.GetMetric() {
		match := true

		for _, label := range metric.GetLabel() {
			if want, has := labels[label.GetName()]; has && want != label.GetValue() {
				match = false
			}
		}

		if match && len(metric.GetLabel()) >= len(labels) {
			if metric.GetGauge() != nil {
				return metric.GetGauge().GetValue(), true
			}

			return metric.GetCounter().GetValue(), true
		}
	}

	return 0, false
}

func count(families map[string]*dto.MetricFamily, name string) int {
	if family, ok := families[name]; ok {
		return len(family.GetMetric())
	}

	return 0
}

func testLogger() log.Logger { return log.NewNopLogger() }

func TestPfcCollectorRackFixture(t *testing.T) {
	rackRedis(t)

	families := gather(t, NewPfcCollector(testLogger()))
	data := rackCounters(t)
	port := data["COUNTERS_PORT_NAME_MAP"]["Ethernet128"]

	// The switch counts the hosts' priority-3 pause frames under priority 2 (finding of the 08 Nov RCA).
	got, ok := value(t, families, "sonic_pfc_received_frames_total", map[string]string{"device": "Ethernet128", "priority": "2"})
	if !ok || got != 9189574 {
		t.Errorf("Ethernet128 priority 2 rx = %v (found %v), want 9189574 from fixture %s", got, ok, port)
	}

	if got, _ := value(t, families, "sonic_pfc_received_frames_total", map[string]string{"device": "Ethernet128", "priority": "3"}); got != 0 {
		t.Errorf("Ethernet128 priority 3 rx = %v, want 0", got)
	}

	if n := count(families, "sonic_pfc_transmitted_frames_total"); n != 16 {
		t.Errorf("transmitted series = %d, want 2 ports x 8 priorities", n)
	}

	if got, _ := value(t, families, "sonic_pfc_collector_success", nil); got != 1 {
		t.Errorf("collector_success = %v", got)
	}
}

func TestPgCollectorDefaultAndAll(t *testing.T) {
	rackRedis(t)

	families := gather(t, NewPgCollector(testLogger()))
	for _, metric := range families["sonic_pg_watermark_bytes"].GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "pg" && label.GetValue() != "3" && label.GetValue() != "4" {
				t.Errorf("default include-pgs exported pg %s", label.GetValue())
			}
		}
	}

	// 2 ports x 2 PGs x 2 watermarks x 3 types
	if n := count(families, "sonic_pg_watermark_bytes"); n != 24 {
		t.Errorf("watermark series = %d, want 24", n)
	}

	previous := *pgInclude
	*pgInclude = "all"

	defer func() { *pgInclude = previous }()

	if n := count(gather(t, NewPgCollector(testLogger())), "sonic_pg_watermark_bytes"); n <= 24 {
		t.Errorf("include all exported %d series, want more than 24", n)
	}
}

func TestBufferPoolAndQueueInfo(t *testing.T) {
	rackRedis(t)

	families := gather(t, NewBufferPoolCollector(testLogger()))
	if _, ok := value(t, families, "sonic_buffer_pool_watermark_bytes", map[string]string{"pool": "ingress_lossless_pool", "type": "periodic"}); !ok {
		t.Error("ingress_lossless_pool periodic watermark missing")
	}

	info := gather(t, NewQueueInfoCollector(testLogger()))
	if _, ok := value(t, info, "sonic_queue_info", map[string]string{"device": "Ethernet0", "queue": "13", "type": "multicast"}); !ok {
		t.Error("queue 13 of Ethernet0 must be multicast")
	}

	if _, ok := value(t, info, "sonic_queue_info", map[string]string{"device": "Ethernet0", "queue": "3", "type": "unicast"}); !ok {
		t.Error("queue 3 of Ethernet0 must be unicast")
	}

	if n := count(info, "sonic_queue_info"); n != 40 {
		t.Errorf("queue_info series = %d, want 40", n)
	}
}

// The incident defect: PFC is enabled for priority 3 and an ingress lossless profile is bound, but there
// is no egress (BUFFER_QUEUE) lossless profile, no WRED/ECN profile and no PFC watchdog.
func TestQosConfigShowsMissingEgressLosslessProfile(t *testing.T) {
	rackRedis(t)

	families := gather(t, NewQosConfigCollector(testLogger()))

	if got, ok := value(t, families, "sonic_port_pfc_enabled", map[string]string{"device": "Ethernet0", "priority": "3"}); !ok || got != 1 {
		t.Errorf("pfc_enabled priority 3 = %v (found %v), want 1", got, ok)
	}

	if got, _ := value(t, families, "sonic_port_pfc_enabled", map[string]string{"device": "Ethernet0", "priority": "0"}); got != 0 {
		t.Errorf("pfc_enabled priority 0 = %v, want 0", got)
	}

	if _, ok := value(t, families, "sonic_buffer_pg_profile_info", map[string]string{"device": "Ethernet0", "pgs": "3-4", "profile": "ingress_lossless_profile"}); !ok {
		t.Error("ingress lossless PG profile must be reported")
	}

	if n := count(families, "sonic_buffer_queue_profile_info"); n != 0 {
		t.Errorf("buffer_queue_profile_info series = %d, want 0 (no egress lossless profile on this switch)", n)
	}

	if n := count(families, "sonic_queue_wred_profile_bound") + count(families, "sonic_pfcwd_configured"); n != 0 {
		t.Errorf("wred/pfcwd series = %d, want 0", n)
	}

	if got, _ := value(t, families, "sonic_port_qos_map_bound", map[string]string{"device": "Ethernet0", "map": "tc_to_pg_map"}); got != 1 {
		t.Errorf("tc_to_pg_map bound = %v, want 1", got)
	}

	if got, ok := value(t, families, "sonic_port_qos_map_bound", map[string]string{"device": "Ethernet0", "map": "dscp_to_tc_map"}); !ok || got != 0 {
		t.Errorf("dscp_to_tc_map bound = %v (found %v), want 0: host-facing ports have no DSCP map", got, ok)
	}

	if got, _ := value(t, families, "sonic_pfcwd_default_enabled", nil); got != 0 {
		t.Errorf("pfcwd default = %v, want 0 (disable)", got)
	}
}

func TestQosConfigWithCompliantConfiguration(t *testing.T) {
	server := rackRedis(t)

	server.Select(4)
	server.HSet("BUFFER_QUEUE|Ethernet0|3-4", "profile", "[BUFFER_PROFILE|egress_lossless_profile]")
	server.HSet("WRED_PROFILE|AZURE_LOSSLESS", "ecn", "ecn_all", "green_min_threshold", "250000")
	server.HSet("QUEUE|Ethernet0|3", "wred_profile", "[WRED_PROFILE|AZURE_LOSSLESS]")
	server.HSet("PFC_WD|Ethernet0", "action", "drop")

	families := gather(t, NewQosConfigCollector(testLogger()))

	for _, check := range []struct {
		name   string
		labels map[string]string
	}{
		{"sonic_buffer_queue_profile_info", map[string]string{"device": "Ethernet0", "queues": "3-4", "profile": "egress_lossless_profile"}},
		{"sonic_queue_wred_profile_bound", map[string]string{"device": "Ethernet0", "queues": "3", "profile": "AZURE_LOSSLESS"}},
		{"sonic_pfcwd_configured", map[string]string{"device": "Ethernet0"}},
		{"sonic_wred_ecn_enabled", map[string]string{"profile": "AZURE_LOSSLESS"}},
	} {
		if got, ok := value(t, families, check.name, check.labels); !ok || got != 1 {
			t.Errorf("%s%v = %v (found %v), want 1", check.name, check.labels, got, ok)
		}
	}
}

func TestEcnPfcwdAbsentThenPresent(t *testing.T) {
	server := rackRedis(t)

	families := gather(t, NewEcnPfcwdCollector(testLogger()))
	if n := count(families, "sonic_queue_ecn_marked_packets_total") + count(families, "sonic_pfcwd_storm_detected_total"); n != 0 {
		t.Errorf("series = %d on a switch without WRED stats and pfcwd, want 0", n)
	}

	if got, _ := value(t, families, "sonic_ecn_pfcwd_collector_success", nil); got != 1 {
		t.Errorf("collector_success = %v, want 1", got)
	}

	queue := rackCounters(t)["COUNTERS_QUEUE_NAME_MAP"]["Ethernet128:3"]
	server.Select(2)
	server.HSet("COUNTERS:"+queue,
		"SAI_QUEUE_STAT_WRED_ECN_MARKED_PACKETS", "42",
		"PFC_WD_STATUS", "stormed",
		"PFC_WD_QUEUE_STATS_DEADLOCK_DETECTED", "2",
		"PFC_WD_QUEUE_STATS_TX_DROPPED_PACKETS", "7")

	families = gather(t, NewEcnPfcwdCollector(testLogger()))

	if got, _ := value(t, families, "sonic_queue_ecn_marked_packets_total", map[string]string{"device": "Ethernet128", "queue": "3"}); got != 42 {
		t.Errorf("ecn marked = %v, want 42", got)
	}

	if got, _ := value(t, families, "sonic_pfcwd_queue_stormed", map[string]string{"device": "Ethernet128", "queue": "3"}); got != 1 {
		t.Errorf("stormed = %v, want 1", got)
	}

	if got, _ := value(t, families, "sonic_pfcwd_dropped_packets_total", map[string]string{"direction": "transmit"}); got != 7 {
		t.Errorf("pfcwd tx dropped = %v, want 7", got)
	}
}

func TestLldpCollector(t *testing.T) {
	server := rackRedis(t)

	server.Select(0)
	server.HSet("LLDP_ENTRY_TABLE:Ethernet168", "lldp_rem_sys_name", "dswn2", "lldp_rem_port_id", "enp216s0f0np0", "lldp_rem_chassis_id", "0c:42:a1:00:00:01")

	families := gather(t, NewLldpCollector(testLogger()))
	if _, ok := value(t, families, "sonic_lldp_neighbor_info", map[string]string{"device": "Ethernet168", "remote_system_name": "dswn2", "remote_port_id": "enp216s0f0np0"}); !ok {
		t.Error("LLDP neighbour not exported")
	}
}

// One value that does not parse must cost only its own series, not the whole queue collector.
func TestQueueCollectorSkipsBadValue(t *testing.T) {
	server := rackRedis(t)

	queue := rackCounters(t)["COUNTERS_QUEUE_NAME_MAP"]["Ethernet0:5"]
	server.Select(2)
	server.HSet("COUNTERS:"+queue, "SAI_QUEUE_STAT_PACKETS", "not-a-number")

	before := testCounterValue(t, scrapeErrors.WithLabelValues("queue", "parse"))
	families := gather(t, NewQueueCollector(testLogger()))

	if got, _ := value(t, families, "sonic_queue_collector_success", nil); got != 1 {
		t.Errorf("collector_success = %v, want 1: a bad value is isolated, not a scrape failure", got)
	}

	if _, ok := value(t, families, "sonic_queue_packets_total", map[string]string{"device": "Ethernet0", "queue": "5"}); ok {
		t.Error("series with the bad value must be skipped")
	}

	if _, ok := value(t, families, "sonic_queue_packets_total", map[string]string{"device": "Ethernet128", "queue": "3"}); !ok {
		t.Error("other queues must still be exported")
	}

	if after := testCounterValue(t, scrapeErrors.WithLabelValues("queue", "parse")); after != before+1 {
		t.Errorf("parse errors %v -> %v, want +1", before, after)
	}
}

func testCounterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()

	metric := &dto.Metric{}
	if err := c.Write(metric); err != nil {
		t.Fatal(err)
	}

	return metric.GetCounter().GetValue()
}

func TestScanAndPipeline(t *testing.T) {
	rackRedis(t)

	rc, err := redis.NewClient()
	if err != nil {
		t.Fatal(err)
	}

	defer rc.Close()

	keys, err := rc.ScanKeys(context.Background(), "CONFIG_DB", "BUFFER_PG|*")
	if err != nil || len(keys) != 4 {
		t.Fatalf("scan: %v keys=%v", err, keys)
	}

	hashes, err := rc.PipelineHGetAll(context.Background(), "CONFIG_DB", keys)
	if err != nil || len(hashes) != 4 {
		t.Fatalf("pipeline: %v", err)
	}

	for _, key := range keys {
		if !strings.HasPrefix(hashes[key]["profile"], "[BUFFER_PROFILE|") {
			t.Errorf("%s: %v", key, hashes[key])
		}
	}
}
