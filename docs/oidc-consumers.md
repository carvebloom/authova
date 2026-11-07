# Browser and server OIDC consumers

Each App has its own canonical issuer, `<Core origin>/a/<app_key>`; never configure
the Core root origin as the issuer. Register each relying party independently.
Use an exact redirect URI, authorization code, S256 PKCE, state and nonce.
Confidential clients use `client_secret_basic`; public SPA clients use `none`.

`allowed_audiences` must be URI values, for example `urn:example:api`.
Ordinary client audiences are fixed at registration; the `resource` request
parameter is reserved for Agent installation resource binding. ID Token audience
is the client ID; API access-token audience is the registered resource audience.
Access tokens use RS256 and `typ=at+jwt`; ID Tokens are not API credentials.
Discover the exact issuer's JWKS and validate signature, issuer, audience and time
claims, and nonce/state for login. Do not infer privileges from email or silently
merge identities: persist an exact `(issuer, subject)` identity and explicitly
assign local application permissions. No groups or system-admin claims are
implicitly exposed to consumers.

## SPA CORS

On discovery, JWKS, token, revocation and UserInfo endpoints, a cross-origin
browser request is admitted only when its exact `Origin` matches the origin of a
redirect registered to an **active public client in that App**. Confidential,
system and disabled clients do not grant browser CORS. Scheme, hostname and port
must match. A registration in another App grants nothing. Cookie-authenticated
login/consent/authorization pages are outside this CORS API surface.

Preflight accepts only the endpoint's methods and `Content-Type`, `Authorization`
and `Accept` headers. Replies vary by Origin/preflight fields and return the exact
allowed origin, never a wildcard or `Access-Control-Allow-Credentials`. Provider
library CORS headers are filtered so the library cannot broaden this policy.
Originless server-to-server traffic and same-Core-origin traffic retain normal
endpoint validation. Database lookup failures fail closed. OAuth client auth,
PKCE, signature and audience checks remain in force; CORS is not authentication.

## Refresh and logout

For rotating refresh tokens, register the `refresh_token` grant and request
`offline_access`. Use the discovered revocation endpoint to revoke this RP's
refresh token. Revocation/reuse semantics are checked by the provider. An API
performing only offline JWT validation cannot observe immediate revocation before
expiry; deploy introspection or another explicit state check if that is required.

There is currently no advertised RP-initiated `end_session_endpoint` or dynamic
post-logout URI registration. RP local logout and token revocation do not imply
cross-application/global logout. Preserve independent local administrator and
recovery access.

## Verification

`go test -race ./internal/protocol` covers exact origins, App-bound reads,
confidential/disabled/foreign origins, methods/headers, UserInfo and fail-closed
outages. Real HTTPS integration exercised discovery, JWKS/preflight, code/PKCE,
mail OTP registration over STARTTLS, stable subjects, separated token audiences,
refresh rotation/revocation, and cross-App CORS rejection. Consumer-side signature,
state/nonce/issuer/audience and permission tests belong to each RP. A browser that
does not trust the deployment CA must fail; neither verification bypasses nor
automatic system trust changes are part of this integration.
