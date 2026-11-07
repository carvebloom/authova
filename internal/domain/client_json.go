package domain

import "encoding/json"

// The public contract specifies arrays, not nullable arrays. Empty legacy
// payloads and zero-length slices must have identical JSON semantics.
func (c Client) MarshalJSON() ([]byte, error) {
	type encodedClient Client
	out := encodedClient(c)
	if out.RedirectURIs == nil {
		out.RedirectURIs = []string{}
	}
	if out.Scopes == nil {
		out.Scopes = []string{}
	}
	if out.Audiences == nil {
		out.Audiences = []string{}
	}
	if out.IntrospectionAudiences == nil {
		out.IntrospectionAudiences = []string{}
	}
	if out.Grants == nil {
		out.Grants = []string{}
	}
	return json.Marshal(out)
}
