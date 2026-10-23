package grpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
)

// v1alphaClient is v1Client for servers that predate
// grpc.reflection.v1. The messages are the same shape.
//
//nolint:staticcheck // v1alpha is deprecated, but servers that predate v1 only speak it.
type v1alphaClient struct {
	stream reflectionv1alpha.ServerReflection_ServerReflectionInfoClient
}

//nolint:staticcheck // v1alpha is deprecated, but servers that predate v1 only speak it.
func openV1alpha(ctx context.Context, conn grpc.ClientConnInterface) (reflClient, error) {
	s, err := reflectionv1alpha.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	return &v1alphaClient{s}, err
}

//nolint:staticcheck // v1alpha is deprecated, but servers that predate v1 only speak it.
func (c *v1alphaClient) do(req *reflectionv1alpha.ServerReflectionRequest) (*reflectionv1alpha.ServerReflectionResponse, error) {
	if err := c.stream.Send(req); err != nil {
		return nil, err
	}
	resp, err := c.stream.Recv()
	if err != nil {
		return nil, err
	}
	if e := resp.GetErrorResponse(); e != nil {
		return nil, status.Error(codes.Code(e.GetErrorCode()), e.GetErrorMessage())
	}
	return resp, nil
}

//nolint:staticcheck // v1alpha is deprecated, but servers that predate v1 only speak it.
func (c *v1alphaClient) listServices() ([]string, error) {
	resp, err := c.do(&reflectionv1alpha.ServerReflectionRequest{
		MessageRequest: &reflectionv1alpha.ServerReflectionRequest_ListServices{},
	})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, s := range resp.GetListServicesResponse().GetService() {
		names = append(names, s.GetName())
	}
	return names, nil
}

//nolint:staticcheck // v1alpha is deprecated, but servers that predate v1 only speak it.
func (c *v1alphaClient) fileContainingSymbol(symbol string) ([][]byte, error) {
	resp, err := c.do(&reflectionv1alpha.ServerReflectionRequest{
		MessageRequest: &reflectionv1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol},
	})
	return resp.GetFileDescriptorResponse().GetFileDescriptorProto(), err
}

//nolint:staticcheck // v1alpha is deprecated, but servers that predate v1 only speak it.
func (c *v1alphaClient) fileByFilename(name string) ([][]byte, error) {
	resp, err := c.do(&reflectionv1alpha.ServerReflectionRequest{
		MessageRequest: &reflectionv1alpha.ServerReflectionRequest_FileByFilename{FileByFilename: name},
	})
	return resp.GetFileDescriptorResponse().GetFileDescriptorProto(), err
}
