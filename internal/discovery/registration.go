// Package discovery registers DistributionCenter with Blade service discovery.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	gen "src.solsynth.dev/sosys/go/proto"
)

// Options describes the instance advertised to Blade.
type Options struct {
	Service           string
	InstanceID        string
	HttpEndpoint      string
	GrpcEndpoint      string
	RegistrationToken string
	LeaseSeconds      int
	Weight            int
}

// Registration owns the register, renew, and deregister lifecycle.
type Registration struct {
	client gen.DyServiceDiscoveryServiceClient
	opts   Options
	log    *slog.Logger

	registered atomic.Bool
}

// New creates a registration controller over an established gRPC connection.
func New(client gen.DyServiceDiscoveryServiceClient, opts Options, log *slog.Logger) *Registration {
	return &Registration{client: client, opts: opts, log: log}
}

// Run retries registration and renews the lease until ctx is cancelled.
func (r *Registration) Run(ctx context.Context) {
	retryDelay := 5 * time.Second
	for {
		interval, err := r.register(ctx)
		if err == nil {
			r.registered.Store(true)
			retryDelay = 5 * time.Second
			r.renewLoop(ctx, interval)
			if ctx.Err() != nil {
				return
			}
		} else if ctx.Err() != nil {
			return
		} else {
			r.registered.Store(false)
			r.log.Warn("blade service discovery registration failed", "service", r.opts.Service, "instance_id", r.opts.InstanceID, "retry_in", retryDelay, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
		retryDelay = time.Duration(math.Min(float64(retryDelay)*2, float64(30*time.Second)))
	}
}

func (r *Registration) register(ctx context.Context) (time.Duration, error) {
	instance := &gen.DyServiceInstance{
		Service:      r.opts.Service,
		InstanceId:   r.opts.InstanceID,
		HttpEndpoint: r.opts.HttpEndpoint,
		GrpcEndpoint: r.opts.GrpcEndpoint,
		Weight:       int32(r.opts.Weight),
	}
	response, err := r.client.Register(r.authorized(ctx), &gen.DyRegisterServiceInstanceRequest{
		Instance:     instance,
		LeaseSeconds: int32(r.opts.LeaseSeconds),
	})
	if err != nil {
		return 0, err
	}
	r.log.Info("registered with blade service discovery", "service", r.opts.Service, "instance_id", r.opts.InstanceID)
	return renewalInterval(response.GetLeaseExpiresAtUnixMs(), r.opts.LeaseSeconds), nil
}

func (r *Registration) renewLoop(ctx context.Context, interval time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		response, err := r.client.Renew(r.authorized(ctx), &gen.DyRenewServiceLeaseRequest{
			Service:      r.opts.Service,
			InstanceId:   r.opts.InstanceID,
			LeaseSeconds: int32(r.opts.LeaseSeconds),
		})
		if err != nil {
			if status.Code(err) == codes.NotFound {
				r.log.Info("blade discovery lease not found; re-registering", "service", r.opts.Service, "instance_id", r.opts.InstanceID)
			} else {
				r.log.Warn("blade discovery lease renewal failed", "service", r.opts.Service, "instance_id", r.opts.InstanceID, "error", err)
			}
			return
		}
		interval = renewalInterval(response.GetLeaseExpiresAtUnixMs(), r.opts.LeaseSeconds)
	}
}

// Deregister removes the lease on graceful shutdown.
func (r *Registration) Deregister(ctx context.Context) {
	if !r.registered.Load() {
		return
	}
	_, err := r.client.Deregister(r.authorized(ctx), &gen.DyDeregisterServiceInstanceRequest{
		Service:    r.opts.Service,
		InstanceId: r.opts.InstanceID,
	})
	if err != nil {
		r.log.Warn("blade service discovery deregistration failed", "service", r.opts.Service, "instance_id", r.opts.InstanceID, "error", err)
		return
	}
	r.registered.Store(false)
}

func (r *Registration) authorized(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+r.opts.RegistrationToken)
}

func renewalInterval(expiresAtUnixMs int64, leaseSeconds int) time.Duration {
	if expiresAtUnixMs > 0 {
		if remaining := time.Until(time.UnixMilli(expiresAtUnixMs)); remaining > 0 {
			if interval := remaining / 3; interval >= time.Second {
				return interval
			}
			return time.Second
		}
	}
	interval := time.Duration(leaseSeconds) * time.Second / 3
	if interval < time.Second {
		return time.Second
	}
	return interval
}

// Validate rejects invalid registration settings before dialing Blade.
func Validate(opts Options) error {
	if strings.TrimSpace(opts.Service) == "" {
		return errConfig("service name is required")
	}
	if strings.TrimSpace(opts.InstanceID) == "" {
		return errConfig("instance ID is required")
	}
	if strings.TrimSpace(opts.HttpEndpoint) == "" && strings.TrimSpace(opts.GrpcEndpoint) == "" {
		return errConfig("at least one endpoint is required")
	}
	if opts.LeaseSeconds < 3 {
		return errConfig("lease must be at least three seconds")
	}
	if opts.Weight < 1 {
		return errConfig("weight must be greater than zero")
	}
	return nil
}

func errConfig(message string) error {
	return fmt.Errorf("service discovery: %w: %s", errors.New("invalid configuration"), message)
}
