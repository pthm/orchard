package grpc

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/pthm/orchard/internal/suggest"
)

// descs is everything the provider knows about the server's services.
type descs struct {
	set      *descriptorpb.FileDescriptorSet // files sorted by name
	files    *protoregistry.Files
	types    *dynamicpb.Types
	services []string // sorted; reflection and health services excluded
}

func newDescs(set *descriptorpb.FileDescriptorSet) (*descs, error) {
	sort.Slice(set.File, func(i, j int) bool { return set.File[i].GetName() < set.File[j].GetName() })
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("descriptors: %w", err)
	}
	d := &descs{set: set, files: files, types: dynamicpb.NewTypes(files)}
	files.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		for i := range f.Services().Len() {
			if name := string(f.Services().Get(i).FullName()); !internalService(name) {
				d.services = append(d.services, name)
			}
		}
		return true
	})
	sort.Strings(d.services)
	return d, nil
}

func internalService(name string) bool {
	return strings.HasPrefix(name, "grpc.reflection.") || strings.HasPrefix(name, "grpc.health.")
}

// readDescriptorSet loads a serialized FileDescriptorSet, as written by
// `buf build -o` or `protoc --descriptor_set_out`.
func readDescriptorSet(path string) (*descs, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("descriptor_set: %w", err)
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("descriptor_set %s isn't a serialized FileDescriptorSet: %w", path, err)
	}
	return newDescs(&set)
}

// method resolves "package.Service/Method" (or grpcurl's
// "package.Service.Method") to a unary method.
func (d *descs) method(name string) (protoreflect.MethodDescriptor, error) {
	svc, m, ok := strings.Cut(name, "/")
	if !ok {
		i := strings.LastIndex(name, ".")
		if i < 0 {
			return nil, fmt.Errorf("method %q isn't package.Service/Method", name)
		}
		svc, m = name[:i], name[i+1:]
	}
	desc, err := d.files.FindDescriptorByName(protoreflect.FullName(svc))
	sd, isService := desc.(protoreflect.ServiceDescriptor)
	if err != nil || !isService {
		return nil, fmt.Errorf("no service %q%s", svc, suggest.Hint(svc, d.services))
	}
	md := sd.Methods().ByName(protoreflect.Name(m))
	if md == nil {
		names := make([]string, sd.Methods().Len())
		for i := range names {
			names[i] = string(sd.Methods().Get(i).Name())
		}
		return nil, fmt.Errorf("service %q has no method %q%s", svc, m, suggest.Hint(m, names))
	}
	kind := ""
	switch {
	case md.IsStreamingClient() && md.IsStreamingServer():
		kind = "bidirectional streaming"
	case md.IsStreamingClient():
		kind = "client-streaming"
	case md.IsStreamingServer():
		kind = "server-streaming"
	}
	if kind != "" {
		return nil, fmt.Errorf("method %q is %s; grpc_call supports unary methods only", m, kind)
	}
	return md, nil
}

// reflect fetches the descriptors of every service the server lists
// over reflection, with their dependencies: grpc.reflection.v1, falling
// back to v1alpha for older servers.
func reflect(ctx context.Context, conn grpc.ClientConnInterface, address string) (*descs, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var c reflClient
	var services []string
	for _, open := range []func(context.Context, grpc.ClientConnInterface) (reflClient, error){openV1, openV1alpha} {
		var err error
		if c, err = open(ctx, conn); err == nil {
			services, err = c.listServices()
		}
		if err == nil {
			break
		}
		if status.Code(err) != codes.Unimplemented {
			return nil, fmt.Errorf("server reflection on %s: %w", address, err)
		}
		c = nil
	}
	if c == nil {
		return nil, fmt.Errorf("%s doesn't offer server reflection; turn it on in the server, or set descriptor_set in the provider config", address)
	}

	files := map[string]*descriptorpb.FileDescriptorProto{}
	add := func(raw [][]byte, err error) error {
		if err != nil {
			return err
		}
		for _, b := range raw {
			fd := &descriptorpb.FileDescriptorProto{}
			if err := proto.Unmarshal(b, fd); err != nil {
				return err
			}
			files[fd.GetName()] = fd
		}
		return nil
	}
	for _, svc := range services {
		if internalService(svc) {
			continue
		}
		if err := add(c.fileContainingSymbol(svc)); err != nil {
			return nil, fmt.Errorf("server reflection on %s: %s: %w", address, svc, err)
		}
	}
	// Servers usually send dependencies along; fetch any they didn't.
	for {
		var missing []string
		for _, fd := range files {
			for _, dep := range fd.GetDependency() {
				if files[dep] == nil {
					missing = append(missing, dep)
				}
			}
		}
		if len(missing) == 0 {
			break
		}
		for _, name := range missing {
			if err := add(c.fileByFilename(name)); err != nil {
				return nil, fmt.Errorf("server reflection on %s: %s: %w", address, name, err)
			}
		}
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, fd := range files {
		set.File = append(set.File, fd)
	}
	return newDescs(set)
}

// reflClient is one reflection stream, over either protocol version.
type reflClient interface {
	listServices() ([]string, error)
	fileContainingSymbol(symbol string) ([][]byte, error)
	fileByFilename(name string) ([][]byte, error)
}

type v1Client struct {
	stream reflectionv1.ServerReflection_ServerReflectionInfoClient
}

func openV1(ctx context.Context, conn grpc.ClientConnInterface) (reflClient, error) {
	s, err := reflectionv1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	return &v1Client{s}, err
}

func (c *v1Client) do(req *reflectionv1.ServerReflectionRequest) (*reflectionv1.ServerReflectionResponse, error) {
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

func (c *v1Client) listServices() ([]string, error) {
	resp, err := c.do(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{},
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

func (c *v1Client) fileContainingSymbol(symbol string) ([][]byte, error) {
	resp, err := c.do(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol},
	})
	return resp.GetFileDescriptorResponse().GetFileDescriptorProto(), err
}

func (c *v1Client) fileByFilename(name string) ([][]byte, error) {
	resp, err := c.do(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_FileByFilename{FileByFilename: name},
	})
	return resp.GetFileDescriptorResponse().GetFileDescriptorProto(), err
}
