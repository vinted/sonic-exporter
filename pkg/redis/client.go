package redis

import (
	"context"
	"errors"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

var redisClients = make(map[string]*redis.Client)

var redisDatabases = map[string]int{
	"APPL_DB":     0,
	"COUNTERS_DB": 2,
	"CONFIG_DB":   4,
	"STATE_DB":    6,
}

type RedisConfig struct {
	Address  string `env:"REDIS_ADDRESS" env-default:"localhost:6379"`
	Password string `env:"REDIS_PASSWORD" env-default:""`
}

var cfg = RedisConfig{}

func connect(dbName string) error {
	if cfg == (RedisConfig{}) { // if config is not initialized
		err := cleanenv.ReadEnv(&cfg)
		if err != nil {
			return errors.New("failed to read redis config")
		}
	}

	redisClients[dbName] = redis.NewClient(&redis.Options{
		Addr:     cfg.Address,
		Password: cfg.Password,
		DB:       redisDatabases[dbName],
	})

	return nil
}

// Issue a HGETALL on key in a selected database
func HgetAllFromDb(dbName, key string) (map[string]string, error) {
	var client *redis.Client

	_, ok := redisDatabases[dbName]

	if ok {
		client, ok = redisClients[dbName]

		if !ok {
			err := connect(dbName)
			if err != nil {
				return nil, err
			}

			client = redisClients[dbName]
		}
		data, err := client.HGetAll(ctx, key).Result()
		return data, err
	}

	return nil, errors.New("database not defined")
}
