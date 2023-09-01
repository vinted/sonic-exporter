package collector

import (
	"context"
	"encoding/json"
	"io"
	"os"
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

func TestInterfaceCollector(t *testing.T) {
	s := miniredis.RunT(t)

	os.Setenv("REDIS_ADDRESS", s.Addr())

	promlogConfig := &promlog.Config{}
	logger := promlog.New(promlogConfig)

	interfaceCollector := NewInterfaceCollector(logger)

	err := populateRedisData()
	if err != nil {
		t.Errorf("failed to populate redis data: %v", err)
	}

	problems, _ := testutil.CollectAndLint(interfaceCollector)
	metricCount := testutil.CollectAndCount(interfaceCollector)
	t.Logf("metric count: %v", metricCount)

	for _, problem := range problems {
		t.Errorf("metric %v has a problem: %v", problem.Metric, problem.Text)
	}
}
