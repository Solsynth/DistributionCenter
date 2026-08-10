package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gin-gonic/gin"
	gen "src.solsynth.dev/sosys/go/proto"

	"src.solsynth.dev/sosys/distribution/internal/config"
	"src.solsynth.dev/sosys/distribution/internal/database"
	"src.solsynth.dev/sosys/distribution/internal/service"
)

type createReleaseRequest struct {
	Version      string                  `json:"version"`
	Channel      string                  `json:"channel,omitempty"`
	Channels     []string                `json:"channels"`
	ReleaseNotes string                  `json:"release_notes"`
	Artifacts    []createArtifactRequest `json:"artifacts"`
}

type createArtifactRequest struct {
	ObjectKey    string `json:"object_key"`
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
}

type uploadURLRequest struct {
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
}

type ReleaseView struct {
	ID           string         `json:"id"`
	ProductID    string         `json:"product_id"`
	Version      string         `json:"version"`
	Channel      string         `json:"channel,omitempty"`
	Channels     []string       `json:"channels"`
	ReleaseNotes string         `json:"release_notes"`
	Status       string         `json:"status"`
	PublishedAt  *time.Time     `json:"published_at"`
	Artifacts    []ArtifactView `json:"artifacts"`
}

type ArtifactView struct {
	ID           string `json:"id"`
	ObjectKey    string `json:"object_key"`
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	Size         int64  `json:"size"`
	Hash         string `json:"hash"`
	DownloadURL  string `json:"download_url"`
}

// RegisterPublisherRoutes registers the publisher-owned catalog contract.
// Authentication is delegated to Sphere; DistributionCenter stores only the
// publisher ID and product metadata needed to address releases.
func RegisterPublisherRoutes(engine *gin.Engine, releases *service.ReleaseService, publishers service.PublisherDirectory, cfg *config.Config) {
	publisher := engine.Group("/api/v1/publishers/:publisherID")
	publisher.GET("/products", listProducts(releases))
	publisher.Use(publisherBearer(releases, publishers, true))
	publisher.POST("/products", createProduct(releases))

	group := engine.Group("/api/v1/products/:productID")
	group.GET("", getProduct(releases))
	group.GET("/releases", listReleases(releases))
	group.GET("/update", resolveUpdate(releases))
	group.GET("/channels", listChannels(releases))

	protected := group.Group("")
	protected.Use(publisherBearer(releases, publishers, false))
	protected.POST("/artifacts/upload-url", prepareUpload(releases))
	protected.POST("/releases", createRelease(releases))
	protected.POST("/releases/:releaseID/publish", publishRelease(releases))
	protected.POST("/releases/:releaseID/yank", yankRelease(releases))
	protected.POST("/channels", createChannel(releases))
	protected.GET("/metrics", metrics(releases))
	_ = cfg
}

type createProductRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func listProducts(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		products, err := releases.ListProducts(c.Request.Context(), c.Param("publisherID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": products})
	}
}

func createProduct(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input createProductRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, errors.Join(service.ErrValidation, err))
			return
		}
		product, err := releases.CreateProduct(c.Request.Context(), c.Param("publisherID"), service.CreateProductInput{Slug: input.Slug, Name: input.Name, Description: input.Description})
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusCreated, product)
	}
}

func getProduct(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		product, publisher, latest, err := releases.GetPublicProduct(c.Request.Context(), c.Param("productID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"product": product, "publisher": publisher, "latest": releaseView(latest, releases)})
	}
}

// RegisterRoutes is the revision-1 app-secret surface kept for existing
// clients. New deployments register RegisterPublisherRoutes instead.
func RegisterRoutes(engine *gin.Engine, releases *service.ReleaseService, apps service.AppDirectory, cfg *config.Config) {
	group := engine.Group("/api/v1/apps/:appID")
	group.GET("", getApp(releases))
	group.GET("/releases", listReleases(releases))
	group.GET("/update", resolveUpdate(releases))
	group.GET("/channels", listChannels(releases))
	protected := group.Group("")
	protected.Use(bearerSecret(apps))
	protected.POST("/artifacts/upload-url", prepareUpload(releases))
	protected.POST("/releases", createRelease(releases))
	protected.POST("/releases/:releaseID/publish", publishRelease(releases))
	protected.POST("/releases/:releaseID/yank", yankRelease(releases))
	protected.POST("/channels", createChannel(releases))
	protected.GET("/metrics", metrics(releases))
	_ = cfg
}

func getApp(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		app, developer, latest, err := releases.GetPublicApp(c.Request.Context(), c.Param("appID"))
		if err != nil {
			writeError(c, err)
			return
		}
		var dev *gen.DyDeveloper
		if developer != nil {
			dev = developer.GetDeveloper()
		}
		c.JSON(http.StatusOK, gin.H{"app": app, "developer": dev, "latest": releaseView(latest, releases)})
	}
}

func listReleases(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		channel := strings.TrimSpace(c.Query("channel"))
		if channel == "" {
			writeError(c, errors.Join(service.ErrValidation, errors.New("channel is required")))
			return
		}
		query := service.ReleaseListQuery{Channel: channel, Platform: c.Query("platform"), Architecture: c.Query("architecture")}
		var err error
		query.Limit, err = queryInt(c, "limit", 20)
		if err != nil {
			writeError(c, err)
			return
		}
		query.Offset, err = queryInt(c, "offset", 0)
		if err != nil {
			writeError(c, err)
			return
		}
		result, err := releases.ListReleases(c.Request.Context(), catalogID(c), query)
		if err != nil {
			writeError(c, err)
			return
		}
		views := make([]ReleaseView, 0, len(result.Data))
		for _, release := range result.Data {
			views = append(views, *releaseView(release, releases))
		}
		c.JSON(http.StatusOK, gin.H{"data": views, "total": result.Total, "limit": result.Limit, "offset": result.Offset})
	}
}

func resolveUpdate(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := service.UpdateQuery{
			CurrentVersion: c.Query("current_version"),
			Channel:        c.Query("channel"),
			Platform:       c.Query("platform"),
			Architecture:   c.Query("architecture"),
			InstallationID: firstNonEmpty(c.Query("installation_id"), c.GetHeader("X-Installation-ID")),
			OSVersion:      firstNonEmpty(c.Query("os_version"), c.GetHeader("X-OS-Version")),
			ClientVersion:  firstNonEmpty(c.Query("client_version"), c.GetHeader("X-Client-Version")),
		}
		if query.CurrentVersion == "" || query.Channel == "" || query.Platform == "" || query.Architecture == "" {
			writeError(c, errors.Join(service.ErrValidation, errors.New("all update query parameters are required")))
			return
		}
		result, err := releases.ResolveUpdate(c.Request.Context(), catalogID(c), query)
		if err != nil {
			writeError(c, err)
			return
		}
		var view *ReleaseView
		if result.Release != nil {
			view = releaseView(result.Release, releases)
		}
		c.JSON(http.StatusOK, gin.H{"update_available": result.UpdateAvailable, "current_version": result.CurrentVersion, "release": view})
	}
}

func listChannels(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels, err := releases.ListChannels(c.Request.Context(), catalogID(c))
		if err != nil {
			writeError(c, err)
			return
		}
		type channelView struct {
			ID          string       `json:"id"`
			Name        string       `json:"name"`
			DisplayName string       `json:"display_name"`
			Description string       `json:"description"`
			Latest      *ReleaseView `json:"latest"`
		}
		views := make([]channelView, 0, len(channels))
		for _, item := range channels {
			views = append(views, channelView{ID: item.Channel.ID, Name: item.Channel.Name, DisplayName: item.Channel.DisplayName, Description: item.Channel.Description, Latest: releaseView(item.Latest, releases)})
		}
		c.JSON(http.StatusOK, gin.H{"data": views})
	}
}

func createChannel(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input service.CreateChannelInput
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, errors.Join(service.ErrValidation, err))
			return
		}
		channel, err := releases.CreateChannel(c.Request.Context(), catalogID(c), input)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusCreated, channel)
	}
}

func metrics(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		from, err := timeParam(c.Query("from"))
		if err != nil {
			writeError(c, err)
			return
		}
		to, err := timeParam(c.Query("to"))
		if err != nil {
			writeError(c, err)
			return
		}
		result, err := releases.UsageMetrics(c.Request.Context(), catalogID(c), from, to)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func prepareUpload(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input uploadURLRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, errors.Join(service.ErrValidation, err))
			return
		}
		upload, err := releases.PrepareArtifactUpload(c.Request.Context(), catalogID(c), service.ArtifactUploadInput{FileName: input.FileName, MimeType: input.MimeType})
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, upload)
	}
}

func createRelease(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input createReleaseRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			writeError(c, errors.Join(service.ErrValidation, err))
			return
		}
		artifacts := make([]service.ArtifactInput, 0, len(input.Artifacts))
		for _, artifact := range input.Artifacts {
			artifacts = append(artifacts, service.ArtifactInput{ObjectKey: artifact.ObjectKey, Platform: artifact.Platform, Architecture: artifact.Architecture})
		}
		release, err := releases.CreateRelease(c.Request.Context(), catalogID(c), service.CreateReleaseInput{Version: input.Version, Channel: input.Channel, Channels: input.Channels, ReleaseNotes: input.ReleaseNotes, Artifacts: artifacts})
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusCreated, releaseView(release, releases))
	}
}

func publishRelease(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		release, err := releases.Publish(c.Request.Context(), catalogID(c), c.Param("releaseID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, releaseView(release, releases))
	}
}

func yankRelease(releases *service.ReleaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		release, err := releases.Yank(c.Request.Context(), catalogID(c), c.Param("releaseID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, releaseView(release, releases))
	}
}

func publisherBearer(releases *service.ReleaseService, publishers service.PublisherDirectory, publisherPath bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		fields := strings.Fields(c.GetHeader("Authorization"))
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			writeError(c, service.ErrUnauthorized)
			c.Abort()
			return
		}
		accountID, err := publishers.Authenticate(c.Request.Context(), fields[1])
		if err != nil || strings.TrimSpace(accountID) == "" {
			if err != nil && status.Code(err) == codes.Unavailable {
				writeError(c, service.ErrDependency)
			} else {
				writeError(c, service.ErrUnauthorized)
			}
			c.Abort()
			return
		}
		publisherID := strings.TrimSpace(c.Param("publisherID"))
		if !publisherPath {
			publisherID, err = releases.ProductPublisherID(c.Param("productID"))
			if err != nil {
				writeError(c, err)
				c.Abort()
				return
			}
		}
		valid, err := publishers.IsPublisherMember(c.Request.Context(), publisherID, accountID, gen.DyPublisherMemberRole_DY_EDITOR)
		if err != nil {
			writeError(c, service.ErrDependency)
			c.Abort()
			return
		}
		if !valid {
			writeError(c, service.ErrForbidden)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(service.WithAccountID(c.Request.Context(), accountID))
		c.Next()
	}
}

func bearerSecret(apps service.AppDirectory) gin.HandlerFunc {
	return func(c *gin.Context) {
		fields := strings.Fields(c.GetHeader("Authorization"))
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			writeError(c, service.ErrUnauthorized)
			c.Abort()
			return
		}
		valid, err := apps.CheckCustomAppSecret(c.Request.Context(), c.Param("appID"), fields[1], false)
		if err != nil {
			writeError(c, service.ErrDependency)
			c.Abort()
			return
		}
		if !valid {
			writeError(c, service.ErrUnauthorized)
			c.Abort()
			return
		}
		c.Next()
	}
}

func writeError(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrValidation):
		code = http.StatusBadRequest
	case errors.Is(err, service.ErrUnauthorized):
		code = http.StatusUnauthorized
	case errors.Is(err, service.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, service.ErrForbidden):
		code = http.StatusForbidden
	case errors.Is(err, service.ErrConflict):
		code = http.StatusConflict
	case errors.Is(err, service.ErrDependency):
		code = http.StatusServiceUnavailable
	}
	message := err.Error()
	if idx := strings.Index(message, ": "); idx >= 0 {
		message = message[idx+2:]
	}
	c.JSON(code, gin.H{"error": message})
}

func queryInt(c *gin.Context, key string, fallback int) (int, error) {
	value := c.Query(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.Join(service.ErrValidation, errors.New(key+" must be an integer"))
	}
	return parsed, nil
}

func releaseView(release *database.Release, store interface{ PublicURL(string) string }) *ReleaseView {
	if release == nil {
		return nil
	}
	view := &ReleaseView{Artifacts: make([]ArtifactView, 0, len(release.Artifacts)), Channels: make([]string, 0, len(release.Channels))}
	view.ID, view.ProductID, view.Version = release.ID, release.AppID, release.Version
	view.Channel, view.ReleaseNotes, view.Status = string(release.Channel), release.ReleaseNotes, string(release.Status)
	for _, channel := range release.Channels {
		view.Channels = append(view.Channels, channel.Name)
	}
	view.PublishedAt = release.PublishedAt
	for _, artifact := range release.Artifacts {
		downloadURL := ""
		if release.Status != database.ReleaseStatusDraft && store != nil {
			downloadURL = store.PublicURL(artifact.ObjectKey)
		}
		view.Artifacts = append(view.Artifacts, ArtifactView{ID: artifact.ID, ObjectKey: artifact.ObjectKey, Platform: artifact.Platform, Architecture: artifact.Architecture, FileName: artifact.FileName, MimeType: artifact.MimeType, Size: artifact.Size, Hash: artifact.Hash, DownloadURL: downloadURL})
	}
	return view
}
func catalogID(c *gin.Context) string {
	if value := c.Param("productID"); value != "" {
		return value
	}
	return c.Param("appID")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func timeParam(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.Join(service.ErrValidation, errors.New("time must be RFC3339"))
	}
	return parsed, nil
}
