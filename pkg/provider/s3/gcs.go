package s3

import (
	"context"
	"fmt"

	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"golang.org/x/oauth2"
)

const (
	gcsEndpoint = "https://storage.googleapis.com"
	gcsScope    = "https://www.googleapis.com/auth/devstorage.read_write"
)

// bearerAuth sends an OAuth token from ts on every request, for GCS's
// XML API with Application Default Credentials. The client is given
// anonymous AWS credentials, so SigV4 signing is skipped and this is
// the only Authorization header.
func bearerAuth(ts oauth2.TokenSource) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Finalize.Add(middleware.FinalizeMiddlewareFunc("GoogleBearerAuth",
			func(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
				req, ok := in.Request.(*smithyhttp.Request)
				if !ok {
					return middleware.FinalizeOutput{}, middleware.Metadata{}, fmt.Errorf("unexpected request type %T", in.Request)
				}
				tok, err := ts.Token()
				if err != nil {
					return middleware.FinalizeOutput{}, middleware.Metadata{}, fmt.Errorf("google application default credentials: %w", err)
				}
				req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
				return next.HandleFinalize(ctx, in)
			}), middleware.After)
	}
}
