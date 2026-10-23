package s3

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
)

// on_conflict values, matching typed SQL inserts and Redis.
const (
	conflictUpdate = "update"
	conflictIgnore = "ignore"
)

// backupPrefix is where an overwritten object is copied before the
// write, so teardown can put it back.
const backupPrefix = ".orchard-backup/"

// written is what the most recent action did, for Inverse.
type written struct {
	bucket        string
	key           string
	createdBucket bool
	createdObject bool
	backup        string // key of the backup copy, for an overwrite
	owned         bool   // an earlier put this run created the object
}

// Inverse describes the undo of the action that just ran: delete a
// bucket or object it created; for an overwrite, put the backup back
// when the object is owned or the scenario asks (managed_teardown =
// true), and otherwise just delete the backup.
func (p *Provider) Inverse(_ string, _, _ map[string]cty.Value, opts provider.InverseOptions) *provider.Invocation {
	w := p.last
	p.last = nil
	switch {
	case w == nil:
		return nil
	case w.createdBucket:
		return &provider.Invocation{Action: "s3_delete_bucket", Attrs: map[string]cty.Value{"bucket": cty.StringVal(w.bucket)}}
	case w.createdObject:
		return deleteInvocation(w.bucket, w.key)
	case w.backup != "" && (w.owned || opts.Requested):
		return &provider.Invocation{Action: "s3_restore", Attrs: map[string]cty.Value{
			"bucket": cty.StringVal(w.bucket),
			"key":    cty.StringVal(w.key),
			"backup": cty.StringVal(w.backup),
		}}
	case w.backup != "":
		return deleteInvocation(w.bucket, w.backup)
	}
	return nil
}

func deleteInvocation(bucket, key string) *provider.Invocation {
	return &provider.Invocation{Action: "s3_delete_object", Attrs: map[string]cty.Value{
		"bucket": cty.StringVal(bucket),
		"key":    cty.StringVal(key),
	}}
}

// status is the HTTP status of a failed request, or 0.
func status(err error) int {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}

func (p *Provider) createBucket(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	bucket := attrs["bucket"].AsString()
	existsOK := true
	if v, ok := attrs["exists_ok"]; ok && v.IsKnown() && !v.IsNull() {
		existsOK = v.True()
	}
	out := func(created bool) (map[string]cty.Value, error) {
		if !created && !existsOK {
			return nil, fmt.Errorf("bucket %q already exists (set exists_ok = true to use it)", bucket)
		}
		p.last = &written{bucket: bucket, createdBucket: created}
		return map[string]cty.Value{"bucket": cty.StringVal(bucket), "created": cty.BoolVal(created)}, nil
	}

	// HeadBucket first: in us-east-1, CreateBucket on a bucket you
	// already own succeeds, so its result can't tell us created.
	_, err := p.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &bucket})
	switch {
	case err == nil:
		return out(false)
	case status(err) != 404:
		return nil, err
	}

	in := &s3.CreateBucketInput{Bucket: &bucket}
	if p.region != "us-east-1" && p.region != "auto" {
		in.CreateBucketConfiguration = &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(p.region),
		}
	}
	var optFns []func(*s3.Options)
	if p.gcs && p.project != "" {
		optFns = append(optFns, func(o *s3.Options) {
			o.APIOptions = append(o.APIOptions, smithyhttp.AddHeaderValue("x-goog-project-id", p.project))
		})
	}
	_, err = p.client.CreateBucket(ctx, in, optFns...)
	var owned *types.BucketAlreadyOwnedByYou
	switch {
	case errors.As(err, &owned):
		return out(false)
	case err != nil:
		return nil, err
	}
	return out(true)
}

// body opens the one content source an s3_put_object sets.
func body(attrs map[string]cty.Value) (r io.ReadSeeker, size int64, closeFn func(), err error) {
	if v, ok := attrs["content"]; ok && !v.IsNull() {
		s := v.AsString()
		return strings.NewReader(s), int64(len(s)), func() {}, nil
	}
	if v, ok := attrs["content_base64"]; ok && !v.IsNull() {
		b, err := base64.StdEncoding.DecodeString(v.AsString())
		if err != nil {
			return nil, 0, nil, fmt.Errorf("content_base64: %w", err)
		}
		return bytes.NewReader(b), int64(len(b)), func() {}, nil
	}
	f, err := os.Open(attrs["file"].AsString())
	if err != nil {
		return nil, 0, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, nil, err
	}
	return f, info.Size(), func() { _ = f.Close() }, nil
}

// copySource is the x-amz-copy-source value for bucket/key: the key
// URL-encoded, keeping its slashes.
func copySource(bucket, key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return bucket + "/" + strings.Join(segs, "/")
}

func (p *Provider) putObject(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	bucket, key := attrs["bucket"].AsString(), attrs["key"].AsString()
	r, size, closeBody, err := body(attrs)
	if err != nil {
		return nil, err
	}
	defer closeBody()

	// HeadObject then PutObject isn't atomic; fine for seed data.
	// If-None-Match: * would be, but not every compatible store has it.
	head, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &bucket, Key: &key})
	exists := err == nil
	if err != nil && status(err) != 404 {
		return nil, err
	}
	out := func(etag *string, size int64, created bool) map[string]cty.Value {
		return map[string]cty.Value{
			"bucket":  cty.StringVal(bucket),
			"key":     cty.StringVal(key),
			"etag":    cty.StringVal(strings.Trim(aws.ToString(etag), `"`)),
			"size":    cty.NumberIntVal(size),
			"created": cty.BoolVal(created),
		}
	}
	if exists && attrs["on_conflict"].IsKnown() && !attrs["on_conflict"].IsNull() && attrs["on_conflict"].AsString() == conflictIgnore {
		return out(head.ETag, aws.ToInt64(head.ContentLength), false), nil
	}

	w := &written{bucket: bucket, key: key, createdObject: !exists}
	if exists {
		w.backup = backupPrefix + p.token + "/" + key
		if _, err := p.client.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     &bucket,
			Key:        &w.backup,
			CopySource: aws.String(copySource(bucket, key)),
		}); err != nil {
			return nil, fmt.Errorf("backing up %s before overwriting it: %w", key, err)
		}
	}

	contentType := ""
	if v, ok := attrs["content_type"]; ok && v.IsKnown() && !v.IsNull() {
		contentType = v.AsString()
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(key))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	in := &s3.PutObjectInput{
		Bucket:        &bucket,
		Key:           &key,
		Body:          r,
		ContentLength: aws.Int64(size),
		ContentType:   &contentType,
	}
	if v, ok := attrs["metadata"]; ok && v.IsKnown() && !v.IsNull() && v.LengthInt() > 0 {
		in.Metadata = map[string]string{}
		for k, mv := range v.AsValueMap() {
			in.Metadata[k] = mv.AsString()
		}
	}
	put, err := p.client.PutObject(ctx, in)
	if err != nil {
		return nil, err
	}

	if p.owned == nil {
		p.owned = map[string]bool{}
	}
	id := bucket + "/" + key
	w.owned = p.owned[id]
	if w.createdObject {
		p.owned[id] = true
	}
	p.last = w
	return out(put.ETag, size, w.createdObject), nil
}

func (p *Provider) deleteObject(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	_, err := p.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(attrs["bucket"].AsString()),
		Key:    aws.String(attrs["key"].AsString()),
	})
	if err != nil {
		return nil, err
	}
	return map[string]cty.Value{}, nil
}

func (p *Provider) deleteBucket(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	if _, err := p.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(attrs["bucket"].AsString())}); err != nil {
		return nil, err
	}
	return map[string]cty.Value{}, nil
}

// restore is the internal s3_restore action, the undo of an overwrite:
// copy the backup back over the key, then delete the backup.
func (p *Provider) restore(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	bucket, key, backup := attrs["bucket"].AsString(), attrs["key"].AsString(), attrs["backup"].AsString()
	if _, err := p.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     &bucket,
		Key:        &key,
		CopySource: aws.String(copySource(bucket, backup)),
	}); err != nil {
		return nil, err
	}
	if _, err := p.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &backup}); err != nil {
		return nil, err
	}
	return map[string]cty.Value{}, nil
}
