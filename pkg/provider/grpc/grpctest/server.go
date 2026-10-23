// Package grpctest serves the test Accounts service (testdata/
// accounts.proto) for builtin/grpc's unit and integration tests. It's
// built on dynamicpb, so there's no generated code: the descriptor set
// is the only artifact.
package grpctest

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// DescriptorSet is the serialized FileDescriptorSet for accounts.proto,
// with its imports.
//
//go:embed testdata/accounts.binpb
var DescriptorSet []byte

// CreatedAt is the created_at of every org the server creates.
var CreatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// Files returns the descriptors in DescriptorSet.
func Files() *protoregistry.Files {
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(DescriptorSet, &set); err != nil {
		panic(err)
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		panic(err)
	}
	return files
}

// Options configures Start.
type Options struct {
	// NoReflection leaves server reflection off, for descriptor-set
	// tests.
	NoReflection bool
	// V1AlphaReflection serves only the old grpc.reflection.v1alpha,
	// like servers from before 2022.
	V1AlphaReflection bool
}

// Start serves Accounts on a random localhost port and returns its
// address and a stop function.
func Start(opts Options) (addr string, stop func(), err error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	s := grpc.NewServer()
	files := Files()
	if err := register(s, files); err != nil {
		return "", nil, err
	}
	reflOpts := reflection.ServerOptions{Services: s, DescriptorResolver: files}
	switch {
	case opts.NoReflection:
	case opts.V1AlphaReflection:
		//nolint:staticcheck // the point is to serve the deprecated version.
		reflectionv1alpha.RegisterServerReflectionServer(s, reflection.NewServer(reflOpts))
	default:
		reflectionv1.RegisterServerReflectionServer(s, reflection.NewServerV1(reflOpts))
	}
	go func() { _ = s.Serve(lis) }()
	return lis.Addr().String(), s.Stop, nil
}

type accounts struct {
	mu   sync.Mutex
	orgs map[string]*dynamicpb.Message
}

func register(s *grpc.Server, files *protoregistry.Files) error {
	d, err := files.FindDescriptorByName("accounts.v1.Accounts")
	if err != nil {
		return err
	}
	sd := d.(protoreflect.ServiceDescriptor)
	a := &accounts{orgs: map[string]*dynamicpb.Message{}}
	handlers := map[string]func(context.Context, protoreflect.MethodDescriptor, *dynamicpb.Message) (proto.Message, error){
		"CreateOrg": a.createOrg,
		"GetOrg":    a.getOrg,
		"Echo": func(_ context.Context, _ protoreflect.MethodDescriptor, req *dynamicpb.Message) (proto.Message, error) {
			return req, nil
		},
		"Fail": func(context.Context, protoreflect.MethodDescriptor, *dynamicpb.Message) (proto.Message, error) {
			st, _ := status.New(codes.InvalidArgument, "name is required").WithDetails(&errdetails.BadRequest{
				FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "name", Description: "must not be empty"}},
			})
			return nil, st.Err()
		},
	}
	desc := grpc.ServiceDesc{ServiceName: string(sd.FullName()), HandlerType: (*any)(nil)}
	for i := range sd.Methods().Len() {
		md := sd.Methods().Get(i)
		h, ok := handlers[string(md.Name())]
		if !ok || md.IsStreamingClient() || md.IsStreamingServer() {
			continue
		}
		desc.Methods = append(desc.Methods, grpc.MethodDesc{
			MethodName: string(md.Name()),
			Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				req := dynamicpb.NewMessage(md.Input())
				if err := dec(req); err != nil {
					return nil, err
				}
				return h(ctx, md, req)
			},
		})
	}
	s.RegisterService(&desc, a)
	return nil
}

// createOrg stores an org built from the request, with id org-<n>. It
// echoes the authorization metadata back as the x-auth header, and
// sends a binary trailer.
func (a *accounts) createOrg(ctx context.Context, md protoreflect.MethodDescriptor, req *dynamicpb.Message) (proto.Message, error) {
	in := md.Input().Fields()
	if req.Get(in.ByName("name")).String() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	resp := dynamicpb.NewMessage(md.Output())
	orgField := md.Output().Fields().ByName("org")
	org := dynamicpb.NewMessage(orgField.Message())
	of := orgField.Message().Fields()

	a.mu.Lock()
	id := fmt.Sprintf("org-%d", len(a.orgs)+1)
	a.orgs[id] = org
	a.mu.Unlock()

	org.Set(of.ByName("id"), protoreflect.ValueOfString(id))
	org.Set(of.ByName("name"), req.Get(in.ByName("name")))
	org.Set(of.ByName("plan"), req.Get(in.ByName("plan")))
	if req.Has(in.ByName("owner")) {
		org.Set(of.ByName("owner"), req.Get(in.ByName("owner")))
	}
	ts := dynamicpb.NewMessage(of.ByName("created_at").Message())
	ts.Set(ts.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(CreatedAt.Unix()))
	org.Set(of.ByName("created_at"), protoreflect.ValueOfMessage(ts))
	resp.Set(orgField, protoreflect.ValueOfMessage(org))

	header := metadata.Pairs("x-request-id", id)
	if in, ok := metadata.FromIncomingContext(ctx); ok && len(in.Get("authorization")) > 0 {
		header.Set("x-auth", in.Get("authorization")[0])
	}
	_ = grpc.SetHeader(ctx, header)
	_ = grpc.SetTrailer(ctx, metadata.Pairs("x-trace-bin", string([]byte{0, 1, 2})))
	return resp, nil
}

func (a *accounts) getOrg(_ context.Context, md protoreflect.MethodDescriptor, req *dynamicpb.Message) (proto.Message, error) {
	id := req.Get(md.Input().Fields().ByName("org_id")).String()
	a.mu.Lock()
	org, ok := a.orgs[id]
	a.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "org %q doesn't exist", id)
	}
	return org, nil
}
