package protocol

import (
	"context"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

type authorizeValidator struct{ *op.Provider }

func (a authorizeValidator) ValidateAuthRequest(ctx context.Context, request *oidc.AuthRequest, storage op.Storage, verifier *op.IDTokenHintVerifier) (string, error) {
	client, e := storage.GetClientByClientID(ctx, request.ClientID)
	if e != nil {
		return "", oidc.ErrInvalidRequestRedirectURI().WithParent(e)
	}
	if e = op.ValidateAuthReqRedirectURI(client, request.RedirectURI, request.ResponseType); e != nil {
		return "", e
	}
	for _, scope := range request.Scopes {
		if !client.IsScopeAllowed(scope) {
			return "", oidc.ErrInvalidScope()
		}
	}
	return op.ValidateAuthRequestClient(ctx, request, client, verifier)
}
