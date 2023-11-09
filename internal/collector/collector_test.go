package collector

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/promlog"
	"github.com/vinted/sonic-exporter/pkg/redis"
)

type redisDatabase struct {
	DbId string                       `json:"id"`
	Data map[string]map[string]string `json:"data"`
}

func populateRedisData() error {
	var ctx = context.Background()

	files := []string{
		"../../fixtures/test/counters_db_data.json",
		"../../fixtures/test/config_db_data.json",
		"../../fixtures/test/appl_db_data.json",
		"../../fixtures/test/state_db_data.json",
	}

	for _, file := range files {
		err := pushDataFromFile(ctx, file)
		if err != nil {
			return err
		}
	}

	return nil
}

func pushDataFromFile(ctx context.Context, fileName string) error {
	var database redisDatabase

	redisClient, _ := redis.NewClient()

	file, _ := os.Open(fileName)
	defer file.Close()

	byteValue, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	err = json.Unmarshal(byteValue, &database)
	if err != nil {
		return err
	}

	for key, values := range database.Data {
		err := redisClient.HsetToDb(ctx, database.DbId, key, values)
		if err != nil {
			return err
		}
	}

	return nil
}

func TestMain(m *testing.M) {
	s, err := miniredis.Run()
	if err != nil {
		log.Printf("failed to start redis: %v", err)
		os.Exit(1)
	}

	os.Setenv("REDIS_ADDRESS", s.Addr())
	err = populateRedisData()
	if err != nil {
		log.Printf("failed to populate redis data: %v", err)
		os.Exit(1)
	}

	exitCode := m.Run()

	s.Close()
	os.Unsetenv("REDIS_ADDRESS")
	os.Exit(exitCode)
}

func TestInterfaceCollector(t *testing.T) {
	promlogConfig := &promlog.Config{}
	logger := promlog.New(promlogConfig)

	interfaceCollector := NewInterfaceCollector(logger)

	problems, err := testutil.CollectAndLint(interfaceCollector)
	if err != nil {
		t.Error("metric lint completed with errors")
	}

	metricCount := testutil.CollectAndCount(interfaceCollector)
	t.Logf("metric count: %v", metricCount)

	for _, problem := range problems {
		t.Errorf("metric %v has a problem: %v", problem.Metric, problem.Text)
	}

	metadata := `
		# HELP sonic_interface_collector_success Whether interface collector succeeded
		# TYPE sonic_interface_collector_success gauge
	`

	expected := `

		sonic_interface_collector_success 1
	`
	success_metric := "sonic_interface_collector_success"

	if err := testutil.CollectAndCompare(interfaceCollector, strings.NewReader(metadata+expected), success_metric); err != nil {
		t.Errorf("unexpected collecting result:\n%s", err)
	}
}

func TestHwCollector(t *testing.T) {
	promlogConfig := &promlog.Config{}
	logger := promlog.New(promlogConfig)

	hwCollector := NewHwCollector(logger)

	problems, err := testutil.CollectAndLint(hwCollector)
	if err != nil {
		t.Error("metric lint completed with errors")
	}

	metricCount := testutil.CollectAndCount(hwCollector)
	t.Logf("metric count: %v", metricCount)

	for _, problem := range problems {
		t.Errorf("metric %v has a problem: %v", problem.Metric, problem.Text)
	}

	metadata := `
		# HELP sonic_hw_collector_success Whether hw collector succeeded
		# TYPE sonic_hw_collector_success gauge
	`

	expected := `

	 sonic_hw_collector_success 1
	`
	success_metric := "sonic_hw_collector_success"

	if err := testutil.CollectAndCompare(hwCollector, strings.NewReader(metadata+expected), success_metric); err != nil {
		t.Errorf("unexpected collecting result:\n%s", err)
	}
}

func TestCrmCollector(t *testing.T) {
	promlogConfig := &promlog.Config{}
	logger := promlog.New(promlogConfig)

	crmCollector := NewCrmCollector(logger)

	problems, err := testutil.CollectAndLint(crmCollector)
	if err != nil {
		t.Error("metric lint completed with errors")
	}

	metricCount := testutil.CollectAndCount(crmCollector)
	t.Logf("metric count: %v", metricCount)

	for _, problem := range problems {
		t.Errorf("metric %v has a problem: %v", problem.Metric, problem.Text)
	}

	metadata := `
		# HELP sonic_crm_collector_success Whether crm collector succeeded
		# TYPE sonic_crm_collector_success gauge
	`

	expected := `

	 sonic_crm_collector_success 1
	`
	success_metric := "sonic_crm_collector_success"

	if err := testutil.CollectAndCompare(crmCollector, strings.NewReader(metadata+expected), success_metric); err != nil {
		t.Errorf("unexpected collecting result:\n%s", err)
	}
}
