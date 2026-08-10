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
	Config         *config.Config
	Database       *database.DB
	HTTPServer     *httpserver.Server
	ReleaseService *service.ReleaseService
	AppDirectory   service.AppDirectory
	ArtifactStore  service.ArtifactStore
	DevelopConn    *grpc.ClientConn
	EventBus       *eventbus.Bus
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
	developConn, err := integration.Dial(cfg.Develop.Target, cfg.Develop.UseTLS, cfg.Develop.TLSSkipVerify)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("dial develop: %w", err)
	}
	artifacts, err := integration.NewS3Store(cfg)
	if err != nil {
		_ = developConn.Close()
		cleanup()
		return nil, fmt.Errorf("initialize s3 artifact store: %w", err)
	}
	apps := integration.NewDevelopAppDirectory(gen.NewDyCustomAppServiceClient(developConn))
	var events *eventbus.Bus
	if cfg.Eventbus.URL != "" {
		events, err = eventbus.Connect(cfg.Eventbus.URL)
		if err != nil {
			_ = developConn.Close()
			cleanup()
			return nil, fmt.Errorf("connect event bus: %w", err)
		}
	}
	releases := service.NewReleaseService(db.DB, apps, artifacts, events)
	releases.ConfigureAnalytics(cfg.Analytics.Enabled, cfg.Analytics.Salt)
	httpServer := httpserver.New(cfg)
	httpserver.RegisterRoutes(httpServer.Engine, releases, apps, cfg)
	return &App{Config: cfg, Database: db, HTTPServer: httpServer, ReleaseService: releases, AppDirectory: apps, ArtifactStore: artifacts, DevelopConn: developConn, EventBus: events}, nil
}

func (a *App) Start(context.Context) error { return nil }

func (a *App) Stop(_ context.Context) error {
	if a == nil {
		return nil
	}
	if a.EventBus != nil {
		a.EventBus.Close()
	}
	if a.DevelopConn != nil {
		_ = a.DevelopConn.Close()
	}
	if a.Database != nil {
		return a.Database.Close()
	}
	return nil
}
