package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/distribution/internal/database"
)

const uploadAPIKeyPrefix = "dcu_"

type CreateUploadAPIKeyInput struct {
	Name string
}

type UploadAPIKeyView struct {
	ID         string     `json:"id"`
	ProductID  string     `json:"product_id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	CreatedBy  string     `json:"created_by"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type uploadAPIKeyContextKey struct{}

func WithUploadAPIKeyProductID(ctx context.Context, productID string) context.Context {
	return context.WithValue(ctx, uploadAPIKeyContextKey{}, strings.TrimSpace(productID))
}

func UploadAPIKeyProductID(ctx context.Context) string {
	value, _ := ctx.Value(uploadAPIKeyContextKey{}).(string)
	return value
}

type CreatedUploadAPIKey struct {
	UploadAPIKeyView
	Key string `json:"key"`
}

func (s *ReleaseService) CreateUploadAPIKey(ctx context.Context, productID string, input CreateUploadAPIKeyInput) (*CreatedUploadAPIKey, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("%w: database unavailable", ErrDependency)
	}
	if err := validateAppID(productID); err != nil {
		return nil, err
	}
	if _, err := s.requireApp(ctx, productID, false); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 {
		return nil, fmt.Errorf("%w: name must be 1-128 characters", ErrValidation)
	}
	secret, err := newUploadAPIKeySecret()
	if err != nil {
		return nil, fmt.Errorf("generate upload API key: %w", err)
	}
	hash := hashUploadAPIKey(secret)
	key := &database.UploadAPIKey{
		ID:         uuid.NewString(),
		ProductID:  strings.TrimSpace(productID),
		Name:       name,
		KeyPrefix:  secret[:12],
		SecretHash: hash,
		CreatedBy:  AccountID(ctx),
	}
	if err := s.db.Create(key).Error; err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: upload API key already exists", ErrConflict)
		}
		return nil, fmt.Errorf("create upload API key: %w", err)
	}
	return &CreatedUploadAPIKey{UploadAPIKeyView: uploadAPIKeyView(key), Key: secret}, nil
}

func (s *ReleaseService) ListUploadAPIKeys(ctx context.Context, productID string) ([]UploadAPIKeyView, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("%w: database unavailable", ErrDependency)
	}
	if err := validateAppID(productID); err != nil {
		return nil, err
	}
	if _, err := s.requireApp(ctx, productID, false); err != nil {
		return nil, err
	}
	var keys []database.UploadAPIKey
	if err := s.db.Where("product_id = ?", strings.TrimSpace(productID)).Order("created_at DESC").Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("list upload API keys: %w", err)
	}
	views := make([]UploadAPIKeyView, 0, len(keys))
	for i := range keys {
		views = append(views, uploadAPIKeyView(&keys[i]))
	}
	return views, nil
}

func (s *ReleaseService) RevokeUploadAPIKey(ctx context.Context, productID, keyID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("%w: database unavailable", ErrDependency)
	}
	if err := validateAppID(productID); err != nil {
		return err
	}
	if _, err := s.requireApp(ctx, productID, false); err != nil {
		return err
	}
	if _, err := uuid.Parse(strings.TrimSpace(keyID)); err != nil {
		return fmt.Errorf("%w: key_id must be a UUID", ErrValidation)
	}
	result := s.db.Model(&database.UploadAPIKey{}).
		Where("id = ? AND product_id = ? AND revoked_at IS NULL", strings.TrimSpace(keyID), strings.TrimSpace(productID)).
		Updates(map[string]any{"revoked_at": time.Now().UTC(), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return fmt.Errorf("revoke upload API key: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var key database.UploadAPIKey
	if err := s.db.Select("id").Where("id = ? AND product_id = ?", strings.TrimSpace(keyID), strings.TrimSpace(productID)).First(&key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: upload API key", ErrNotFound)
		}
		return fmt.Errorf("load upload API key: %w", err)
	}
	return nil
}

// CheckUploadAPIKey verifies a key for exactly one product. The secret is
// never persisted or returned after creation.
func (s *ReleaseService) CheckUploadAPIKey(ctx context.Context, productID, secret string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("%w: database unavailable", ErrDependency)
	}
	if err := validateAppID(productID); err != nil {
		return false, err
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return false, nil
	}
	var key database.UploadAPIKey
	if err := s.db.Where("product_id = ? AND secret_hash = ? AND revoked_at IS NULL", strings.TrimSpace(productID), hashUploadAPIKey(secret)).First(&key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("check upload API key: %w", err)
	}
	// Usage telemetry must not turn a valid credential into an outage.
	now := time.Now().UTC()
	_ = s.db.Model(&database.UploadAPIKey{}).Where("id = ?", key.ID).Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error
	return true, nil
}

func newUploadAPIKeySecret() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return uploadAPIKeyPrefix + base64.RawURLEncoding.EncodeToString(random[:]), nil
}

func hashUploadAPIKey(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

func uploadAPIKeyView(key *database.UploadAPIKey) UploadAPIKeyView {
	return UploadAPIKeyView{ID: key.ID, ProductID: key.ProductID, Name: key.Name, KeyPrefix: key.KeyPrefix, CreatedBy: key.CreatedBy, LastUsedAt: key.LastUsedAt, RevokedAt: key.RevokedAt, CreatedAt: key.CreatedAt}
}
