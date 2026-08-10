package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/distribution/internal/database"
)

func (s *ReleaseService) CreateChannel(ctx context.Context, appID string, input CreateChannelInput) (*database.Channel, error) {
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	name, err := validateChannelName(input.Name)
	if err != nil {
		return nil, err
	}
	descriptions, err := normalizeDescriptions(input.Descriptions)
	if err != nil {
		return nil, err
	}
	var existing database.Channel
	if err := s.db.Where("app_id = ? AND name = ?", appID, name).First(&existing).Error; err == nil {
		return nil, fmt.Errorf("%w: channel already exists", ErrConflict)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("load channel: %w", err)
	}
	channel := &database.Channel{ID: uuid.NewString(), AppID: appID, Name: name, DisplayName: strings.TrimSpace(input.DisplayName), Description: input.Description}
	if channel.DisplayName == "" {
		channel.DisplayName = name
	}
	if err := s.db.Create(channel).Error; err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: channel already exists", ErrConflict)
		}
		return nil, fmt.Errorf("create channel: %w", err)
	}
	if err := replaceLocalizations(s.db, localizationChannel, channel.ID, map[string]database.LocalizedText{localizationDescription: descriptions}); err != nil {
		return nil, err
	}
	channel.Descriptions = descriptions
	return channel, nil
}

func (s *ReleaseService) ListChannels(ctx context.Context, appID string) ([]ChannelSummary, error) {
	if _, err := s.requireApp(ctx, appID, true); err != nil {
		return nil, err
	}
	var channels []*database.Channel
	if err := s.db.Where("app_id = ?", appID).Order("name ASC").Find(&channels).Error; err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	if err := hydrateChannelLocalizations(s.db, channels); err != nil {
		return nil, err
	}
	result := make([]ChannelSummary, 0, len(channels))
	for _, channel := range channels {
		latest, err := s.latest(appID, channel.Name)
		if err != nil {
			return nil, err
		}
		result = append(result, ChannelSummary{Channel: channel, Latest: latest})
	}
	return result, nil
}

func (s *ReleaseService) ensureChannels(appID string, names []string) ([]database.Channel, error) {
	channels := make([]database.Channel, 0, len(names))
	for _, name := range names {
		var channel database.Channel
		err := s.db.Where("app_id = ? AND name = ?", appID, name).First(&channel).Error
		if err == nil {
			channels = append(channels, channel)
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load channel: %w", err)
		}
		if !isBuiltinChannel(name) {
			return nil, fmt.Errorf("%w: channel %s must be created before use", ErrNotFound, name)
		}
		channel = database.Channel{ID: uuid.NewString(), AppID: appID, Name: name, DisplayName: name}
		if err := s.db.Create(&channel).Error; err != nil && !isUniqueConstraint(err) {
			return nil, fmt.Errorf("create built-in channel: %w", err)
		}
		if err := s.db.Where("app_id = ? AND name = ?", appID, name).First(&channel).Error; err != nil {
			return nil, fmt.Errorf("load built-in channel: %w", err)
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

func normalizeChannels(values []string, singular string) ([]string, error) {
	all := make([]string, 0, len(values)+1)
	if strings.TrimSpace(singular) != "" {
		all = append(all, singular)
	}
	all = append(all, values...)
	seen := make(map[string]struct{}, len(all))
	result := make([]string, 0, len(all))
	for _, value := range all {
		name, err := validateChannelName(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("%w: duplicate channel %s", ErrConflict, name)
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: at least one channel is required", ErrValidation)
	}
	return result, nil
}

func validChannelName(value string) bool {
	_, err := validateChannelName(value)
	return err == nil
}

func (s *ReleaseService) channelForApp(appID, name string) (*database.Channel, error) {
	var channel database.Channel
	if err := s.db.Where("app_id = ? AND name = ?", appID, name).First(&channel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: channel %s", ErrNotFound, name)
		}
		return nil, fmt.Errorf("load channel: %w", err)
	}
	return &channel, nil
}

func validateChannelName(value string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	if name == "" || len(name) > 64 {
		return "", fmt.Errorf("%w: channel name must be 1-64 characters", ErrValidation)
	}
	for index, char := range name {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.'
		if !valid || ((index == 0 || index == len(name)-1) && (char == '-' || char == '_' || char == '.')) {
			return "", fmt.Errorf("%w: channel name contains invalid characters", ErrValidation)
		}
	}
	return name, nil
}

func isBuiltinChannel(name string) bool {
	switch database.ReleaseChannel(name) {
	case database.ReleaseChannelStable, database.ReleaseChannelBeta, database.ReleaseChannelNightly:
		return true
	default:
		return false
	}
}
