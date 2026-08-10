// DistributionCenter is a Solar Network service scaffold. It exposes the
// shared health/capability contracts and is ready for domain routes.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	gen "src.solsynth.dev/sosys/go/proto"

	"src.solsynth.dev/sosys/distribution/internal/config"
	"src.solsynth.dev/sosys/distribution/internal/discovery"
	"src.solsynth.dev/sosys/distribution/internal/grpcserver"
	"src.solsynth.dev/sosys/distribution/internal/httpserver"
)

var (
	version   = "dev"
	gitCommit = "unknown"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("LOG_LEVEL"))}))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("distribution center exited with error", "error", err)
		os.Exit(1)
	}
}

func parseLogLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error":
		return slog.LevelError
	case "warn", "warning":
		return slog.LevelWarn
	case "info":
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Version == "dev" && version != "dev" {
		cfg.Version = version
	}
	log.Info("distribution center starting", "service", cfg.ServiceName, "version", cfg.Version, "commit", gitCommit)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpSurface := httpserver.New(cfg)
	httpSurface.SetReady(false)

	grpcOptions := make([]grpc.ServerOption, 0, 1)
	if cfg.GRPC.UseTLS {
		if cfg.GRPC.CertFile == "" || cfg.GRPC.KeyFile == "" {
			return errors.New("grpc tls requires grpc.certFile and grpc.keyFile")
		}
		cert, err := tls.LoadX509KeyPair(cfg.GRPC.CertFile, cfg.GRPC.KeyFile)
		if err != nil {
			return fmt.Errorf("load grpc tls credentials: %w", err)
		}
		grpcOptions = append(grpcOptions, grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})))
	}
	grpcSurface, healthSurface := grpcserver.New(grpcOptions...)
	reflection.Register(grpcSurface)
	healthSurface.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	httpListener, err := net.Listen("tcp", listenAddress(cfg.HTTP.Port))
	if err != nil {
		return fmt.Errorf("listen http: %w", err)
	}
	grpcListener, err := net.Listen("tcp", listenAddress(cfg.GRPC.Port))
	if err != nil {
		_ = httpListener.Close()
		return fmt.Errorf("listen grpc: %w", err)
	}

	var discoveryReg *discovery.Registration
	var discoveryConn *grpc.ClientConn
	if cfg.Discovery.Enabled && strings.TrimSpace(cfg.Discovery.Target) != "" {
		opts := discovery.Options{
			Service:           cfg.Discovery.Service,
			InstanceID:        cfg.Discovery.InstanceID,
			HttpEndpoint:      cfg.Discovery.HttpEndpoint,
			GrpcEndpoint:      cfg.Discovery.GrpcEndpoint,
			RegistrationToken: cfg.Discovery.RegistrationToken,
			LeaseSeconds:      cfg.Discovery.LeaseSeconds,
			Weight:            cfg.Discovery.Weight,
		}
		if opts.InstanceID == "" {
			opts.InstanceID = uuid.NewString()
		}
		if opts.HttpEndpoint == "" {
			opts.HttpEndpoint = "http://" + opts.Service + ":" + cfg.HTTP.Port
		}
		if opts.GrpcEndpoint == "" {
			opts.GrpcEndpoint = opts.Service + ":" + cfg.GRPC.Port
		}
		if err := discovery.Validate(opts); err != nil {
			return fmt.Errorf("discovery: %w", err)
		}
		discoveryConn, err = grpc.NewClient(cfg.Discovery.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Warn("blade discovery unavailable; registration disabled", "target", cfg.Discovery.Target, "error", err)
		} else {
			discoveryReg = discovery.New(gen.NewDyServiceDiscoveryServiceClient(discoveryConn), opts, log)
			go discoveryReg.Run(ctx)
			log.Info("blade service discovery enabled", "service", opts.Service, "instance_id", opts.InstanceID, "target", cfg.Discovery.Target)
		}
	} else if cfg.Discovery.Enabled {
		log.Warn("blade discovery enabled without a target; registration disabled")
	}

	httpSurface.SetReady(true)
	httpSrv := &http.Server{Handler: httpSurface.Engine}
	errCh := make(chan error, 2)
	go func() {
		log.Info("http server listening", "addr", httpListener.Addr().String())
		errCh <- httpSrv.Serve(httpListener)
	}()
	go func() {
		log.Info("grpc server listening", "addr", grpcListener.Addr().String())
		errCh <- grpcSurface.Serve(grpcListener)
	}()

	var serveErr error
	select {
	case <-ctx.Done():
		log.Info("shutdown requested")
	case serveErr = <-errCh:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("server stopped unexpectedly", "error", serveErr)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	grpcSurface.GracefulStop()
	if discoveryReg != nil {
		discoveryReg.Deregister(shutdownCtx)
	}
	if discoveryConn != nil {
		_ = discoveryConn.Close()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}

func listenAddress(port string) string {
	port = strings.TrimSpace(port)
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}
