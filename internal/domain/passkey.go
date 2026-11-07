package domain

import "time"

// Passkey stores a versioned, encrypted credential record. The public API only
// exposes its safe display metadata, never credential IDs or public key data.
type Passkey struct {
	ID, UserID, CredentialHash, Name string
	Record                           []byte
	Version                          int64
	CreatedAt, UpdatedAt             time.Time
}

// PasskeyCeremony binds an expiring one-use WebAuthn session to a flow or an
// authenticated account session. Record is a versioned application DTO.
type PasskeyCeremony struct {
	ID, Kind, FlowID, SessionID, UserID string
	Epoch, Expires                      int64
	Used                                bool
	Record                              []byte
}
