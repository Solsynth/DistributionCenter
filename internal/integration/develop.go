package integration

import (
	"context"

	gen "src.solsynth.dev/sosys/go/proto"
)

type DevelopAppDirectory struct {
	client gen.DyCustomAppServiceClient
}

func NewDevelopAppDirectory(client gen.DyCustomAppServiceClient) *DevelopAppDirectory {
	return &DevelopAppDirectory{client: client}
}

func (d *DevelopAppDirectory) GetCustomApp(ctx context.Context, appID string) (*gen.DyCustomApp, error) {
	resp, err := d.client.GetCustomApp(ctx, &gen.DyGetCustomAppRequest{
		Query: &gen.DyGetCustomAppRequest_Id{Id: appID},
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	return resp.GetApp(), nil
}

func (d *DevelopAppDirectory) GetAppDeveloper(ctx context.Context, appID string) (*gen.DyGetAppDeveloperResponse, error) {
	return d.client.GetAppDeveloper(ctx, &gen.DyGetAppDeveloperRequest{AppId: appID})
}

func (d *DevelopAppDirectory) CheckCustomAppSecret(ctx context.Context, appID, secret string, isOIDC bool) (bool, error) {
	resp, err := d.client.CheckCustomAppSecret(ctx, &gen.DyCheckCustomAppSecretRequest{
		SecretIdentifier: &gen.DyCheckCustomAppSecretRequest_AppId{AppId: appID},
		Secret:           secret,
		IsOidc:           &isOIDC,
	})
	if err != nil {
		return false, err
	}
	return resp != nil && resp.GetValid(), nil
}
