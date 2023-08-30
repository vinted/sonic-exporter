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
	var (
		countersDatabase, configDatabase redisDatabase
		ctx                              = context.Background()
	)

	redisClient, _ := redis.NewClient()

	countersDbFile, _ := os.Open("../../fixtures/test/counters_db_data.json")
	defer countersDbFile.Close()

	byteValue, err := io.ReadAll(countersDbFile)
	if err != nil {
		return err
	}
	err = json.Unmarshal(byteValue, &countersDatabase)
	if err != nil {
		return err
	}

	for key, values := range countersDatabase.Data {
		err := redisClient.HsetToDb(ctx, countersDatabase.DbId, key, values)
		if err != nil {
			return err
		}
	}

	configDbFile, _ := os.Open("../../fixtures/test/config_db_data.json")
	defer configDbFile.Close()

	byteValue, err = io.ReadAll(configDbFile)
	if err != nil {
		return err
	}
	err = json.Unmarshal(byteValue, &configDatabase)
	if err != nil {
		return err
	}

	for key, values := range configDatabase.Data {
		err := redisClient.HsetToDb(ctx, configDatabase.DbId, key, values)
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
	for _, problem := range problems {
		t.Errorf("metric %v has a problem: %v", problem.Metric, problem.Text)
	}
}
