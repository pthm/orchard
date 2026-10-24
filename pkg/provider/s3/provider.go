// Package s3 is the builtin Orchard provider for Amazon S3 and
// S3-compatible object stores (MinIO, Cloudflare R2, LocalStack), and
// for Google Cloud Storage through its S3-compatible XML API.
package s3

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/zclconf/go-cty/cty"
	"golang.org/x/oauth2/google"

	"github.com/pthm/orchard/pkg/protocol"
	"github.com/pthm/orchard/pkg/provider"
)

//go:embed schema.hcl
var schemaHCL []byte

// Provider is the S3 provider implementation.
//
// A zero Provider is valid but not yet usable — Configure must be
// called before Execute. Schema works on a zero Provider so the engine
// can validate scenarios before connecting.
type Provider struct {
	schema  protocol.ProviderSchema
	client  *s3.Client
	region  string
	gcs     bool
	project string
	// token names this run's backups: .orchard-backup/<token>/<key>.
	token string
	// owned records the objects (bucket/key) typed puts created this
	// run, so a later overwrite of them is restored on teardown by
	// default. mu guards it.
	mu    sync.Mutex
	owned map[string]bool
}

// New returns an unconfigured S3 provider.
func New() *Provider {
	return &Provider{schema: protocol.MustParseHCL(schemaHCL)}
}

var (
	_ provider.Provider      = (*Provider)(nil)
	_ provider.ActionPlanner = (*Provider)(nil)
	_ provider.Undoer        = (*Provider)(nil)
)

// Schema returns the embedded provider schema.
func (p *Provider) Schema() protocol.ProviderSchema {
	return p.schema
}

func str(cfg map[string]cty.Value, name string) string {
	if v, ok := cfg[name]; ok && v.IsKnown() && !v.IsNull() && v.Type() == cty.String {
		return v.AsString()
	}
	return ""
}

func boolean(cfg map[string]cty.Value, name string) bool {
	v, ok := cfg[name]
	return ok && v.IsKnown() && !v.IsNull() && v.Type() == cty.Bool && v.True()
}

// Configure builds the client. It doesn't call S3: there's no cheap
// request that proves credentials work for buckets the scenario hasn't
// named, so the first action reports credential errors.
func (p *Provider) Configure(ctx context.Context, cfg map[string]cty.Value) error {
	p.gcs = boolean(cfg, "gcs")
	p.project = str(cfg, "project")
	if p.project != "" && !p.gcs {
		return fmt.Errorf("s3: project is only used with gcs = true")
	}
	p.region = str(cfg, "region")
	if p.region == "" {
		p.region = "us-east-1"
		if p.gcs {
			p.region = "auto"
		}
	}
	endpoint := str(cfg, "endpoint")
	if endpoint == "" && p.gcs {
		endpoint = gcsEndpoint
	}
	keyID, secret := str(cfg, "access_key_id"), str(cfg, "secret_access_key")
	if (keyID == "") != (secret == "") {
		return fmt.Errorf("s3: set access_key_id and secret_access_key together")
	}

	opts := []func(*config.LoadOptions) error{config.WithRegion(p.region)}
	if profile := str(cfg, "profile"); profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	var apiOptions []func(*s3.Options)
	switch {
	case keyID != "":
		static := credentials.NewStaticCredentialsProvider(keyID, secret, str(cfg, "session_token"))
		opts = append(opts, config.WithCredentialsProvider(static))
	case p.gcs:
		creds, err := google.FindDefaultCredentials(ctx, gcsScope)
		if err != nil {
			return fmt.Errorf("s3: google application default credentials: %w", err)
		}
		opts = append(opts, config.WithCredentialsProvider(aws.AnonymousCredentials{}))
		apiOptions = append(apiOptions, func(o *s3.Options) {
			o.APIOptions = append(o.APIOptions, bearerAuth(creds.TokenSource))
		})
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return fmt.Errorf("s3: %w", err)
	}
	p.client = s3.NewFromConfig(awsCfg, append(apiOptions, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = boolean(cfg, "path_style") || p.gcs
		// Recent SDKs checksum every request by default, and some
		// S3-compatible stores reject the headers.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})...)

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	p.token = hex.EncodeToString(b)
	return nil
}

// Execute dispatches to the handler for the given action.
func (p *Provider) Execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	out, _, err := p.execute(ctx, action, attrs)
	return out, err
}

// ExecuteUndo runs an action like Execute and also describes its undo
// (see written.undo).
func (p *Provider) ExecuteUndo(ctx context.Context, action string, attrs map[string]cty.Value, opts provider.InverseOptions) (map[string]cty.Value, *provider.Invocation, error) {
	out, w, err := p.execute(ctx, action, attrs)
	if err != nil {
		return nil, nil, err
	}
	return out, w.undo(opts), nil
}

// execute runs an action, returning what it wrote (nil if nothing to
// undo).
func (p *Provider) execute(ctx context.Context, action string, attrs map[string]cty.Value) (map[string]cty.Value, *written, error) {
	if p.client == nil {
		return nil, nil, fmt.Errorf("s3: not configured")
	}
	if err := check(action, attrs); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", action, err)
	}
	var (
		out map[string]cty.Value
		w   *written
		err error
	)
	switch action {
	case "s3_create_bucket":
		out, w, err = p.createBucket(ctx, attrs)
	case "s3_put_object":
		out, w, err = p.putObject(ctx, attrs)
	case "s3_delete_object":
		out, err = p.deleteObject(ctx, attrs)
	case "s3_delete_bucket":
		out, err = p.deleteBucket(ctx, attrs)
	case "s3_restore":
		// Internal: the undo ExecuteUndo records for an overwrite. It
		// isn't in the schema, so scenarios can't call it.
		out, err = p.restore(ctx, attrs)
	default:
		return nil, nil, fmt.Errorf("s3: unknown action %q", action)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", action, err)
	}
	return out, w, nil
}

// PlanAction runs the checks that need no network, at plan and before
// a run. Output types are static in the schema.
func (p *Provider) PlanAction(_ context.Context, action string, attrs map[string]cty.Value) (provider.Plan, error) {
	return provider.Plan{}, check(action, attrs)
}

// Close is a no-op: the SDK client holds no connections that need
// closing.
func (p *Provider) Close() error { return nil }

// bucketName is loose enough for S3 and GCS (which allows underscores
// and longer dotted names): it catches capitals, spaces and slashes.
var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$`)

// check validates an action's attributes without the network. Unknown
// values (references to other actions, at plan time) are skipped.
func check(action string, attrs map[string]cty.Value) error {
	if b, ok := attrs["bucket"]; ok && b.IsKnown() && !b.IsNull() && !bucketName.MatchString(b.AsString()) {
		return fmt.Errorf("bucket %q isn't a valid bucket name: 3 or more lowercase letters, digits, dots and hyphens", b.AsString())
	}
	if action != "s3_put_object" {
		return nil
	}
	if mode, ok := attrs["on_conflict"]; ok && mode.IsKnown() && !mode.IsNull() {
		if m := mode.AsString(); m != conflictUpdate && m != conflictIgnore {
			return fmt.Errorf("on_conflict must be %q or %q; got %q", conflictUpdate, conflictIgnore, m)
		}
	}
	sources := 0
	for _, name := range []string{"content", "content_base64", "file"} {
		if v, ok := attrs[name]; ok && (!v.IsKnown() || !v.IsNull()) {
			sources++
		}
	}
	if sources != 1 {
		return fmt.Errorf("set exactly one of content, content_base64 and file")
	}
	if v, ok := attrs["content_base64"]; ok && v.IsKnown() && !v.IsNull() {
		if _, err := base64.StdEncoding.DecodeString(v.AsString()); err != nil {
			return fmt.Errorf("content_base64 isn't valid base64: %w", err)
		}
	}
	return nil
}
