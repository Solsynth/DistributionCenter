package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gen "src.solsynth.dev/sosys/go/proto"
)

const (
	// Product lifecycle permissions reuse the well-known app product nodes.
	PermissionProductsCreate = "app.products.create"
	PermissionProductsUpdate = "app.products.update"
	PermissionProductsDelete = "app.products.delete"

	// Distribution-specific nodes keep release operations independently
	// grantable instead of treating publisher membership as blanket access.
	PermissionArtifactsUpload  = "distribution.artifacts.upload"
	PermissionReleasesManage   = "distribution.releases.manage"
	PermissionReleasesPublish  = "distribution.releases.publish"
	PermissionChannelsManage   = "distribution.channels.manage"
	PermissionUploadKeysManage = "distribution.upload_keys.manage"
	PermissionMetricsRead      = "distribution.metrics.read"
)

var ErrPermissionDenied = errors.New("permission denied")

// PermissionChecker is the narrow Stargate permission-service contract used
// by route authorization. Keeping it small makes policy tests independent of
// a live auth service.
type PermissionChecker interface {
	HasPermission(context.Context, string, string) (bool, error)
}

type grpcPermissionChecker struct {
	client gen.DyPermissionServiceClient
}

func (c grpcPermissionChecker) HasPermission(ctx context.Context, accountID, key string) (bool, error) {
	response, err := c.client.HasPermission(ctx, &gen.DyHasPermissionRequest{
		Actor: accountID,
		Key:   key,
	})
	if err != nil {
		return false, err
	}
	return response != nil && response.GetHasPermission(), nil
}

// SetPermissionChecker configures the Stargate permission-node checker.
// A nil checker preserves standalone route tests and local fixtures.
func (s *ReleaseService) SetPermissionChecker(checker PermissionChecker) {
	if s != nil {
		s.permissionChecker = checker
	}
}

func (s *ReleaseService) SetPermissionClient(client gen.DyPermissionServiceClient) {
	if client == nil {
		s.SetPermissionChecker(nil)
		return
	}
	s.SetPermissionChecker(grpcPermissionChecker{client: client})
}

// RequireAccountPermission authorizes one global permission node for an
// authenticated account. Publisher membership remains a separate, product-
// scoped check in the HTTP middleware.
func (s *ReleaseService) RequireAccountPermission(ctx context.Context, accountID, key string) error {
	accountID = strings.TrimSpace(accountID)
	key = strings.TrimSpace(key)
	if accountID == "" || key == "" {
		return fmt.Errorf("%w: account id and permission key are required", ErrPermissionDenied)
	}
	if s == nil || s.permissionChecker == nil {
		return nil
	}
	allowed, err := s.permissionChecker.HasPermission(ctx, accountID, key)
	if err != nil {
		return fmt.Errorf("%w: check permission %q: %v", ErrDependency, key, err)
	}
	if !allowed {
		return fmt.Errorf("%w: %s is required", ErrPermissionDenied, key)
	}
	return nil
}
