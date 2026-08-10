// Package grpcserver exposes the shared Solar Network gRPC contracts.
package grpcserver

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/emptypb"

	gen "src.solsynth.dev/sosys/go/proto"
)

// CapabilityName is the feature advertised to Blade's /meta aggregator.
const CapabilityName = "distribution"

type capabilitiesService struct {
	gen.UnimplementedDyCapabilitiesServiceServer
}

func (capabilitiesService) GetCapabilities(context.Context, *emptypb.Empty) (*gen.DyCapabilitiesResponse, error) {
	return &gen.DyCapabilitiesResponse{
		Capabilities: []*gen.DyCapabilityState{
			{
				Name:         CapabilityName,
				Enabled:      true,
				Revision:     1,
				Experimental: false,
			},
		},
		ApiRevision:     1,
		MinimumRevision: 0,
	}, nil
}

// New constructs the gRPC server with standard health and capability services.
func New(options ...grpc.ServerOption) (*grpc.Server, *health.Server) {
	server := grpc.NewServer(options...)
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	gen.RegisterDyCapabilitiesServiceServer(server, capabilitiesService{})
	return server, healthServer
}
