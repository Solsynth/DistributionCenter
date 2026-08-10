package integration

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	gen "src.solsynth.dev/sosys/go/proto"
)

// SphereDirectory is the narrow control-plane contract used by DistributionCenter.
// Sphere authenticates user tokens and owns publisher membership; DistributionCenter
// never infers publisher ownership from a local account or custom app record.
type SphereDirectory struct {
	auth       gen.DyAuthServiceClient
	publishers gen.DyPublisherServiceClient
}

func NewSphereDirectory(auth gen.DyAuthServiceClient, publishers gen.DyPublisherServiceClient) *SphereDirectory {
	return &SphereDirectory{auth: auth, publishers: publishers}
}

func (s *SphereDirectory) Authenticate(ctx context.Context, token string) (string, error) {
	if s == nil || s.auth == nil {
		return "", fmt.Errorf("sphere auth client is unavailable")
	}
	resp, err := s.auth.Authenticate(ctx, &gen.DyAuthenticateRequest{Token: token})
	if err != nil {
		return "", err
	}
	if resp == nil || !resp.GetValid() || resp.GetSession() == nil || resp.GetSession().GetAccountId() == "" {
		return "", fmt.Errorf("invalid authentication token")
	}
	return resp.GetSession().GetAccountId(), nil
}

func (s *SphereDirectory) GetPublisher(ctx context.Context, publisherRef string) (*gen.DyPublisher, error) {
	if s == nil || s.publishers == nil {
		return nil, fmt.Errorf("sphere publisher client is unavailable")
	}
	request := &gen.DyGetPublisherRequest{}
	if _, err := uuid.Parse(publisherRef); err == nil {
		request.Query = &gen.DyGetPublisherRequest_Id{Id: publisherRef}
	} else {
		request.Query = &gen.DyGetPublisherRequest_Name{Name: publisherRef}
	}
	resp, err := s.publishers.GetPublisher(ctx, request)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	return resp.GetPublisher(), nil
}

func (s *SphereDirectory) IsPublisherMember(ctx context.Context, publisherID, accountID string, role gen.DyPublisherMemberRole) (bool, error) {
	if s == nil || s.publishers == nil {
		return false, fmt.Errorf("sphere publisher client is unavailable")
	}
	resp, err := s.publishers.IsPublisherMember(ctx, &gen.DyIsPublisherMemberRequest{PublisherId: publisherID, AccountId: accountID, Role: role})
	if err != nil {
		return false, err
	}
	return resp != nil && resp.GetValid(), nil
}

var _ interface {
	Authenticate(context.Context, string) (string, error)
	GetPublisher(context.Context, string) (*gen.DyPublisher, error)
	IsPublisherMember(context.Context, string, string, gen.DyPublisherMemberRole) (bool, error)
} = (*SphereDirectory)(nil)
