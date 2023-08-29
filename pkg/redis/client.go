package redis

import (
	"context"
	"errors"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/redis/go-redis/v9"
)

type Client struct {
	databases map[string]*redis.Client
	config    RedisConfig
}

var redisDatabases = map[string]int{
	"APPL_DB":     0,
	"COUNTERS_DB": 2,
	"CONFIG_DB":   4,
	"STATE_DB":    6,
}

type RedisConfig struct {
	Address  string `env:"REDIS_ADDRESS" env-default:"localhost:6379"`
	Password string `env:"REDIS_PASSWORD" env-default:""`
	Network  string `env:"REDIS_NETWORK" env-default:"tcp"`
}

func NewClient() (Client, error) {
	var cfg RedisConfig
	c := Client{}

	err := cleanenv.ReadEnv(&cfg)
	if err != nil {
		return c, errors.New("failed to read redis config")
	}

	c.config = cfg
	c.databases = make(map[string]*redis.Client)

	return c, nil
}

func (c *Client) connect(dbName string) error {
	c.databases[dbName] = redis.NewClient(&redis.Options{
		Network:  c.config.Network,
		Addr:     c.config.Address,
		Password: c.config.Password,
		DB:       redisDatabases[dbName],
	})

	return nil
}

// Issue a HGETALL on key in a selected database
func (c Client) HgetAllFromDb(ctx context.Context, dbName, key string) (map[string]string, error) {
	var client *redis.Client

	_, ok := redisDatabases[dbName]

	if ok {
		client, ok = c.databases[dbName]

		if !ok {
			err := c.connect(dbName)
			if err != nil {
				return nil, err
			}

			client = c.databases[dbName]
		}
		data, err := client.HGetAll(ctx, key).Result()
		return data, err
	}

	return nil, errors.New("database not defined")
}
