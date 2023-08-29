package redis

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

var ctx = context.Background()

func TestHgetAll(t *testing.T) {
	s := miniredis.RunT(t)

	os.Setenv("REDIS_ADDRESS", s.Addr())

	redisClient, _ := NewClient()

	for _, dbName := range []string{"APPL_DB", "COUNTERS_DB", "CONFIG_DB", "STATE_DB"} {
		var expectedResult = map[string]string{"key1": "value1", "key2": "value2"}

		dbId, _ := RedisDbId(dbName)
		for key, value := range expectedResult {
			s.DB(dbId).HSet("hash1", key, value)
		}

		result, err := redisClient.HgetAllFromDb(ctx, dbName, "hash1")
		if err != nil {
			fmt.Println(err)
		}

		if !reflect.DeepEqual(result, expectedResult) {
			t.Errorf("data read is not as expected")
		}
	}
}
