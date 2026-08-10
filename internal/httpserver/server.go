// Package httpserver exposes the DistributionCenter HTTP surface.
package httpserver

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"src.solsynth.dev/sosys/distribution/internal/config"
)

// Server owns the HTTP router and readiness state.
type Server struct {
	Engine *gin.Engine
	ready  atomic.Bool
}

// New builds the health and metadata endpoints used by local operators and
// Blade service discovery. Domain routes can be registered under /api later.
func New(cfg *config.Config) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Logger(), gin.Recovery())

	server := &Server{Engine: engine}
	engine.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service": cfg.ServiceName,
			"version": cfg.Version,
		})
	})
	engine.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/alive", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/ready", func(c *gin.Context) {
		if !server.ready.Load() {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		c.Status(http.StatusOK)
	})
	return server
}

// SetReady changes the readiness probe result.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}
