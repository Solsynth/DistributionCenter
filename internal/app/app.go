package app

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	gen "src.solsynth.dev/sosys/go/proto"

	"src.solsynth.dev/sosys/distribution/internal/config"
	"src.solsynth.dev/sosys/distribution/internal/database"
	"src.solsynth.dev/sosys/distribution/internal/eventbus"
	"src.solsynth.dev/sosys/distribution/internal/httpserver"
	"src.solsynth.dev/sosys/distribution/internal/integration"
	"src.solsynth.dev/sosys/distribution/internal/service"
)

type App struct {
	Config             *config.Config
	Database           *database.DB
	HTTPServer         *httpserver.Server
	ReleaseService     *service.ReleaseService
	PublisherDirectory service.PublisherDirectory
	ArtifactStore      service.ArtifactStore
	SphereConn         *grpc.ClientConn
	AuthConn           *grpc.ClientConn
	EventBus           *eventbus.Bus
}

func New(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	db, err := database.Open(cfg)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = db.Close() }
	if err := db.AutoMigrate(); err != nil {
		cleanup()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	authConn, err := integration.Dial(cfg.Auth.Target, cfg.Auth.UseTLS, cfg.Auth.TLSSkipVerify)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("dial auth: %w", err)
	}
	sphereConn, err := integration.Dial(cfg.Sphere.Target, cfg.Sphere.UseTLS, cfg.Sphere.TLSSkipVerify)
	if err != nil {
		_ = authConn.Close()
		cleanup()
		return nil, fmt.Errorf("dial sphere: %w", err)
	}
	publishers := integration.NewSphereDirectory(
		gen.NewDyAuthServiceClient(authConn),
		gen.NewDyPublisherServiceClient(sphereConn),
	)
	artifacts, err := integration.NewS3Store(cfg)
	if err != nil {
		_ = sphereConn.Close()
		_ = authConn.Close()
		cleanup()
		return nil, fmt.Errorf("initialize s3 artifact store: %w", err)
	}
	var events *eventbus.Bus
	if cfg.Eventbus.URL != "" {
		events, err = eventbus.Connect(cfg.Eventbus.URL)
		if err != nil {
			_ = sphereConn.Close()
			_ = authConn.Close()
			cleanup()
			return nil, fmt.Errorf("connect event bus: %w", err)
		}
	}
	releases := service.NewPublisherReleaseService(db.DB, publishers, artifacts, events)
	releases.ConfigureAnalytics(cfg.Analytics.Enabled, cfg.Analytics.Salt)
	releases.ConfigureArtifactRetention(cfg.Releases.ArtifactRetention)
	httpServer := httpserver.New(cfg)
	httpserver.RegisterPublisherRoutes(httpServer.Engine, releases, publishers, cfg)
	return &App{Config: cfg, Database: db, HTTPServer: httpServer, ReleaseService: releases, PublisherDirectory: publishers, ArtifactStore: artifacts, SphereConn: sphereConn, AuthConn: authConn, EventBus: events}, nil
}

func (a *App) Start(context.Context) error { return nil }

func (a *App) Stop(_ context.Context) error {
	if a == nil {
		return nil
	}
	if a.EventBus != nil {
		a.EventBus.Close()
	}
	if a.AuthConn != nil {
		_ = a.AuthConn.Close()
	}
	if a.SphereConn != nil {
		_ = a.SphereConn.Close()
	}
	if a.Database != nil {
		return a.Database.Close()
	}
	return nil
}
