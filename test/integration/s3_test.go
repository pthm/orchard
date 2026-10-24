package integration_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
	s3provider "github.com/pthm/orchard/pkg/provider/s3"
)

// TestS3_RunAndTeardown runs the S3 scenario against SeaweedFS's S3
// gateway (MinIO no longer publishes container images): a created
// bucket with a file, an inline object and a base64 PNG, a key wired
// into Postgres, and overwrites of owned, requested and unowned
// objects. Teardown deletes what was created, restores the owned and
// requested objects from their backups, leaves the unowned one as
// written, and leaves no backups behind.
func TestS3_RunAndTeardown(t *testing.T) {
	ctx := context.Background()
	resetDB(t)
	endpoint := s3Server(t)
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		Region:       "us-east-1",
		UsePathStyle: true,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "minioadmin", SecretAccessKey: "minioadmin"}, nil
		}),
	})
	put := func(bucket, key, body string) {
		_, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: strings.NewReader(body)})
		require.NoError(t, err)
	}
	get := func(bucket, key string) (string, string) {
		out, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
		require.NoError(t, err, "%s/%s", bucket, key)
		defer out.Body.Close()
		b, err := io.ReadAll(out.Body)
		require.NoError(t, err)
		return string(b), aws.ToString(out.ContentType)
	}
	keys := func(bucket string) []string {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &bucket})
		require.NoError(t, err)
		ks := make([]string, 0, len(out.Contents))
		for _, o := range out.Contents {
			ks = append(ks, aws.ToString(o.Key))
		}
		return ks
	}

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("existing")})
	require.NoError(t, err)
	put("existing", "requested.txt", "theirs")
	put("existing", "unowned.txt", "theirs")
	put("existing", "ignored.txt", "theirs!")

	const scenario = "testdata/scenarios/s3.hcl"
	vars := map[string]string{"dsn": testDSN, "endpoint": endpoint}
	out, rec := runScenarioWithRecord(t, scenario, vars, t.TempDir())

	id := ctyInt(t, out["merchant_id"])
	importKey := fmt.Sprintf("imports/%d.csv", id)
	assert.Equal(t, importKey, ctyString(t, out["import_key"]))
	fixture, err := os.ReadFile("testdata/fixtures/products.csv")
	require.NoError(t, err)
	assert.Equal(t, int64(len(fixture)), ctyInt(t, out["import_size"]))
	body, ctype := get("uploads", importKey)
	assert.Equal(t, string(fixture), body)
	assert.Equal(t, "text/csv; charset=utf-8", ctype, "content type from the key's extension")
	assert.NotEmpty(t, ctyString(t, out["pixel_etag"]))
	_, ctype = get("uploads", "pixel.png")
	assert.Equal(t, "image/png", ctype)

	assert.True(t, out["bucket_created"].True())
	assert.False(t, out["existing_created"].True())
	assert.False(t, out["owned_created"].True())
	assert.False(t, out["ignored_created"].True())
	assert.Equal(t, int64(7), ctyInt(t, out["ignored_size"]))

	body, _ = get("uploads", "notes/readme")
	assert.Equal(t, "changed", body)
	body, _ = get("existing", "ignored.txt")
	assert.Equal(t, "theirs!", body)

	var region string
	require.NoError(t, testPool.QueryRow(ctx, "SELECT region FROM merchants WHERE id = $1", id).Scan(&region))
	assert.Equal(t, importKey, region, "the key was wired into Postgres")

	runTeardown(t, scenario, vars, rec)

	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("uploads")})
	assert.Error(t, err, "created bucket should be deleted")
	body, _ = get("existing", "requested.txt")
	assert.Equal(t, "theirs", body, "requested object restored")
	body, _ = get("existing", "unowned.txt")
	assert.Equal(t, "ours", body, "unowned object left as written")
	assert.ElementsMatch(t, []string{"ignored.txt", "requested.txt", "unowned.txt"}, keys("existing"), "no backups left")
}

// TestS3_ConcurrentExecuteUndo: concurrent puts each get the undo for
// their own object.
func TestS3_ConcurrentExecuteUndo(t *testing.T) {
	ctx := context.Background()
	p := s3provider.New()
	require.NoError(t, p.Configure(ctx, map[string]cty.Value{
		"endpoint":          cty.StringVal(s3Server(t)),
		"path_style":        cty.True,
		"access_key_id":     cty.StringVal("minioadmin"),
		"secret_access_key": cty.StringVal("minioadmin"),
	}))
	t.Cleanup(func() { _ = p.Close() })
	_, _, err := p.ExecuteUndo(ctx, "s3_create_bucket", map[string]cty.Value{"bucket": cty.StringVal("concurrent")}, provider.InverseOptions{})
	require.NoError(t, err)

	const n = 10
	undos := make([]*provider.Invocation, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, undos[i], errs[i] = p.ExecuteUndo(ctx, "s3_put_object", map[string]cty.Value{
				"bucket":  cty.StringVal("concurrent"),
				"key":     cty.StringVal(fmt.Sprintf("k%d", i)),
				"content": cty.StringVal("x"),
			}, provider.InverseOptions{})
		}()
	}
	wg.Wait()
	for i, u := range undos {
		require.NoError(t, errs[i])
		require.NotNil(t, u, "k%d", i)
		assert.Equal(t, "s3_delete_object", u.Action)
		assert.Equal(t, fmt.Sprintf("k%d", i), u.Attrs["key"].AsString())
	}
}

var (
	s3Once sync.Once
	s3URL  string
	s3Err  error
)

// s3Server starts a SeaweedFS container with its S3 gateway on first
// use and returns the endpoint. With no identities configured it
// accepts any credentials. The container is terminated after every
// test has run.
func s3Server(t *testing.T) string {
	t.Helper()
	s3Once.Do(func() {
		ctx := context.Background()
		ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "chrislusf/seaweedfs:latest",
				Cmd:          []string{"server", "-s3", "-dir=/data"},
				ExposedPorts: []string{"8333/tcp"},
				WaitingFor: wait.ForHTTP("/").WithPort("8333/tcp").
					WithStatusCodeMatcher(func(int) bool { return true }),
			},
			Started: true,
		})
		if err != nil {
			s3Err = fmt.Errorf("starting seaweedfs container: %w", err)
			return
		}
		afterAll = append(afterAll, func() { _ = testcontainers.TerminateContainer(ctr) })
		host, err := ctr.Host(ctx)
		if err != nil {
			s3Err = err
			return
		}
		port, err := ctr.MappedPort(ctx, "8333/tcp")
		if err != nil {
			s3Err = err
			return
		}
		s3URL = fmt.Sprintf("http://%s:%s", host, port.Port())
	})
	require.NoError(t, s3Err)
	return s3URL
}
