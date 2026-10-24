package integration_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/zclconf/go-cty/cty"
)

// TestRedis_Seed seeds a string key with a TTL, a hash and a list
// against a real Redis, keyed by a Postgres insert's id.
func TestRedis_Seed(t *testing.T) {
	ctx := context.Background()
	resetDB(t)
	url := redisServer(t)
	opts, err := goredis.ParseURL(url)
	require.NoError(t, err)
	rdb := goredis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	require.NoError(t, rdb.FlushDB(ctx).Err())

	out := runScenario(t, "testdata/scenarios/redis.hcl", map[string]string{"dsn": testDSN, "redis_url": url})

	id := ctyInt(t, out["merchant_id"])
	key := fmt.Sprintf("session:%d", id)
	assert.Equal(t, key, ctyString(t, out["session_key"]))
	assert.True(t, out["session_created"].True())
	assert.True(t, out["new_fields"].RawEquals(cty.ListVal([]cty.Value{cty.StringVal("limit"), cty.StringVal("new_checkout")})), "new_fields: %#v", out["new_fields"])
	assert.True(t, out["queue_length"].RawEquals(cty.NumberIntVal(1)))

	got, err := rdb.Get(ctx, key).Result()
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`{"merchant_id":%d,"roles":["admin"]}`, id), got)
	ttl, err := rdb.TTL(ctx, key).Result()
	require.NoError(t, err)
	assert.InDelta(t, time.Hour.Seconds(), ttl.Seconds(), 60)

	flags, err := rdb.HGetAll(ctx, fmt.Sprintf("flags:%d", id)).Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"new_checkout": "true", "limit": "10"}, flags)

	job, err := rdb.LIndex(ctx, "jobs:welcome", 0).Result()
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`{"merchant_id":%d}`, id), job)
}

// TestRedis_Teardown checks automatic teardown against real Redis: a
// created key is deleted, an owned key and a requested one are restored
// from DUMP (value and expiry), an unowned overwritten string is left,
// and an unowned hash loses only the fields the scenario added.
func TestRedis_Teardown(t *testing.T) {
	ctx := context.Background()
	url := redisServer(t)
	opts, err := goredis.ParseURL(url)
	require.NoError(t, err)
	rdb := goredis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	require.NoError(t, rdb.FlushDB(ctx).Err())
	require.NoError(t, rdb.Set(ctx, "requested", "theirs", 10*time.Minute).Err())
	require.NoError(t, rdb.Set(ctx, "unowned", "theirs", 0).Err())
	require.NoError(t, rdb.HSet(ctx, "hash", "a", "theirs").Err())
	require.NoError(t, rdb.HSet(ctx, "hash_requested", "a", "theirs").Err())

	const scenario = "testdata/scenarios/redis_teardown.hcl"
	vars := map[string]string{"redis_url": url}
	_, rec := runScenarioWithRecord(t, scenario, vars, t.TempDir())
	got, err := rdb.Get(ctx, "created").Result()
	require.NoError(t, err)
	assert.Equal(t, "v2", got)

	runTeardown(t, scenario, rec)

	n, err := rdb.Exists(ctx, "created").Result()
	require.NoError(t, err)
	assert.Zero(t, n, "created key should be deleted")

	assert.Equal(t, "theirs", rdb.Get(ctx, "requested").Val())
	ttl := rdb.TTL(ctx, "requested").Val()
	assert.True(t, ttl > 9*time.Minute && ttl <= 10*time.Minute, "requested ttl restored: %v", ttl)

	assert.Equal(t, "ours", rdb.Get(ctx, "unowned").Val(), "unowned key is left as written")
	assert.Equal(t, map[string]string{"a": "ours"}, rdb.HGetAll(ctx, "hash").Val(), "only the new field is removed")
	assert.Equal(t, map[string]string{"a": "theirs"}, rdb.HGetAll(ctx, "hash_requested").Val(), "requested hash restored")
}

var (
	redisOnce sync.Once
	redisURL  string
	redisErr  error
)

// redisServer starts a Redis 7 container on first use and returns its
// URL. The container is terminated after every test has run.
func redisServer(t *testing.T) string {
	t.Helper()
	redisOnce.Do(func() {
		ctx := context.Background()
		ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "redis:7",
				ExposedPorts: []string{"6379/tcp"},
				WaitingFor:   wait.ForLog("Ready to accept connections"),
			},
			Started: true,
		})
		if err != nil {
			redisErr = fmt.Errorf("starting redis container: %w", err)
			return
		}
		afterAll = append(afterAll, func() { _ = testcontainers.TerminateContainer(ctr) })
		host, err := ctr.Host(ctx)
		if err != nil {
			redisErr = err
			return
		}
		port, err := ctr.MappedPort(ctx, "6379/tcp")
		if err != nil {
			redisErr = err
			return
		}
		redisURL = fmt.Sprintf("redis://%s:%s/0", host, port.Port())
	})
	require.NoError(t, redisErr)
	return redisURL
}
