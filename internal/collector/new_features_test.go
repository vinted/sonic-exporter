package collector

import (
	"testing"
)

func TestQueueLimitFromProfileAndDefault(t *testing.T) {
	server := rackRedis(t)

	// No BUFFER_QUEUE profile on the rack fixture: nothing without a default.
	if n := count(gather(t, NewQueueLimitCollector(testLogger())), "sonic_queue_limit_bytes"); n != 0 {
		t.Errorf("limit series without profile or default = %d, want 0", n)
	}

	server.Select(4)
	server.HSet("BUFFER_PROFILE|egress_lossless_profile", "size", "5000000", "pool", "[BUFFER_POOL|egress_lossless_pool]")
	server.HSet("BUFFER_QUEUE|Ethernet0|3-4", "profile", "[BUFFER_PROFILE|egress_lossless_profile]")

	families := gather(t, NewQueueLimitCollector(testLogger()))
	if got, ok := value(t, families, "sonic_queue_limit_bytes", map[string]string{"device": "Ethernet0", "queue": "3", "source": "profile"}); !ok || got != 5000000 {
		t.Errorf("profile limit = %v (found %v), want 5000000", got, ok)
	}

	previous := *queueLimitDefaultBytes
	*queueLimitDefaultBytes = 10314304

	defer func() { *queueLimitDefaultBytes = previous }()

	families = gather(t, NewQueueLimitCollector(testLogger()))
	if got, ok := value(t, families, "sonic_queue_limit_bytes", map[string]string{"device": "Ethernet128", "queue": "3", "source": "default"}); !ok || got != 10314304 {
		t.Errorf("default limit = %v (found %v), want 10314304", got, ok)
	}

	if _, ok := value(t, families, "sonic_queue_limit_bytes", map[string]string{"device": "Ethernet0", "queue": "3", "source": "default"}); ok {
		t.Error("a queue with a profile must not also get the default")
	}
}

func TestQosMapExportsDscpMapAndBinding(t *testing.T) {
	rackRedis(t)

	families := gather(t, NewQosMapCollector(testLogger()))

	// The AZURE DSCP map of the real switch maps DSCP 0 to traffic class 3, the lossless class.
	if _, ok := value(t, families, "sonic_dscp_to_tc", map[string]string{"name": "AZURE", "dscp": "0", "tc": "3"}); !ok {
		t.Error("DSCP 0 -> TC 3 of the AZURE map not exported")
	}

	if n := count(families, "sonic_dscp_to_tc"); n != 64 {
		t.Errorf("dscp_to_tc series = %d, want 64", n)
	}

	if _, ok := value(t, families, "sonic_port_qos_map_info", map[string]string{"device": "Ethernet0", "type": "tc_to_pg_map", "name": "AZURE"}); !ok {
		t.Error("tc_to_pg_map binding of Ethernet0 not exported")
	}
}

func TestLastRefreshTimestamps(t *testing.T) {
	rackRedis(t)

	families := gather(t, NewPfcCollector(testLogger()))
	if got, ok := value(t, families, "sonic_pfc_last_refresh_timestamp_seconds", nil); !ok || got < 1.7e9 {
		t.Errorf("pfc refresh timestamp = %v (found %v)", got, ok)
	}

	families = gather(t, NewQueueCollector(testLogger()))
	if got, ok := value(t, families, "sonic_queue_last_refresh_timestamp_seconds", nil); !ok || got < 1.7e9 {
		t.Errorf("queue refresh timestamp = %v (found %v)", got, ok)
	}

	families = gather(t, NewInterfaceCollector(testLogger()))
	if _, ok := value(t, families, "sonic_interface_last_refresh_timestamp_seconds", nil); !ok {
		t.Error("interface refresh timestamp missing")
	}
}

func TestPgAllPriorityGroupsPeriodicOnly(t *testing.T) {
	rackRedis(t)

	previousInclude, previousTypes := *pgInclude, *pgTypes
	*pgInclude, *pgTypes = "0-7", "periodic"

	defer func() { *pgInclude, *pgTypes = previousInclude, previousTypes }()

	families := gather(t, NewPgCollector(testLogger()))

	// 2 ports x 8 PGs x 2 watermarks x 1 type
	if n := count(families, "sonic_pg_watermark_bytes"); n != 32 {
		t.Errorf("watermark series = %d, want 32", n)
	}

	for _, metric := range families["sonic_pg_watermark_bytes"].GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "type" && label.GetValue() != "periodic" {
				t.Errorf("type %s exported although only periodic was requested", label.GetValue())
			}
		}
	}
}
