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

func RedisDbId(name string) (int, bool) {
	switch name {
	case "APPL_DB":
		return 0, true
	case "COUNTERS_DB":
		return 2, true
	case "CONFIG_DB":
		return 4, true
	case "STATE_DB":
		return 6, true
	}

	return 0, false
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
	dbId, ok := RedisDbId(dbName)
	if ok {
		c.databases[dbName] = redis.NewClient(&redis.Options{
			Network:  c.config.Network,
			Addr:     c.config.Address,
			Password: c.config.Password,
			DB:       dbId,
		})
		return nil
	}

	return errors.New("database not defined")
}

func (c *Client) selectClient(dbName string) (*redis.Client, error) {
	var client *redis.Client

	_, ok := RedisDbId(dbName)

	if ok {
		client, ok = c.databases[dbName]

		if !ok {
			err := c.connect(dbName)
			if err != nil {
				return nil, err
			}

			client = c.databases[dbName]
		}

		return client, nil
	}

	return nil, errors.New("database not defined")
}

// Issue a HGETALL on key in a selected database
func (c Client) HgetAllFromDb(ctx context.Context, dbName, key string) (map[string]string, error) {
	client, err := c.selectClient(dbName)
	if err != nil {
		return nil, err
	}

	data, err := client.HGetAll(ctx, key).Result()
	return data, err
}

func (c Client) HsetToDb(ctx context.Context, dbName, key string, data map[string]string) error {
	client, err := c.selectClient(dbName)
	if err != nil {
		return err
	}

	client.HSet(ctx, key, data)

	return nil
}

// KeysFromDb returns all keys matching pattern. It uses SCAN, which does not block redis like KEYS.
func (c Client) KeysFromDb(ctx context.Context, dbName, pattern string) ([]string, error) {
	return c.ScanKeys(ctx, dbName, pattern)
}

// ScanKeys iterates the keyspace with SCAN (COUNT 1000) and returns every key matching pattern.
func (c Client) ScanKeys(ctx context.Context, dbName, pattern string) ([]string, error) {
	client, err := c.selectClient(dbName)
	if err != nil {
		return nil, err
	}

	var (
		keys   []string
		cursor uint64
	)

	for {
		batch, next, err := client.Scan(ctx, cursor, pattern, 1000).Result()
		if err != nil {
			return nil, err
		}

		keys = append(keys, batch...)
		cursor = next

		if cursor == 0 {
			return keys, nil
		}
	}
}

const pipelineBatch = 512

// PipelineHGetAll issues HGETALL for every key in pipelined batches and returns the hashes by key.
// Keys that do not exist map to an empty hash.
func (c Client) PipelineHGetAll(ctx context.Context, dbName string, keys []string) (map[string]map[string]string, error) {
	client, err := c.selectClient(dbName)
	if err != nil {
		return nil, err
	}

	result := make(map[string]map[string]string, len(keys))

	for start := 0; start < len(keys); start += pipelineBatch {
		end := start + pipelineBatch
		if end > len(keys) {
			end = len(keys)
		}

		pipe := client.Pipeline()
		cmds := make([]*redis.MapStringStringCmd, 0, end-start)

		for _, key := range keys[start:end] {
			cmds = append(cmds, pipe.HGetAll(ctx, key))
		}

		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}

		for i, cmd := range cmds {
			result[keys[start+i]] = cmd.Val()
		}
	}

	return result, nil
}

func (c Client) Close() {
	for name, client := range c.databases {
		client.Close()
		delete(c.databases, name)
	}
}
