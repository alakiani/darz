package protocol

import "time"

// Messages of the panel's Identity API (ADR 0003). Panel calls carry a
// session cookie; requests that change something must be JSON.

// InviteToken names an Invite. Tokens travel in bodies, never in URLs.
type InviteToken struct {
	Token string `json:"token"`
}

// InviteInfo is what an Invite offers, shown before it is accepted.
type InviteInfo struct {
	Kind      string    `json:"kind"`             // "new" or "recovery"
	Handle    string    `json:"handle,omitempty"` // the User a recovery Invite is for
	Admin     bool      `json:"admin"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AcceptInvite redeems an Invite. Handle is chosen on a new-User Invite and
// ignored on a recovery one.
type AcceptInvite struct {
	Token    string `json:"token"`
	Handle   string `json:"handle,omitempty"`
	Password string `json:"password"`
}

// Login starts a session. TOTPCode is required when the User has TOTP on.
type Login struct {
	Handle   string `json:"handle"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"`
}

// Me is the signed-in User.
type Me struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Admin       bool   `json:"admin"`
	TOTPEnabled bool   `json:"totp_enabled"`
	// MFA is set when this session proved a second factor; Admin actions
	// need it.
	MFA         bool `json:"mfa"`
	DeviceCap   int  `json:"device_cap"`
	DeviceCount int  `json:"device_count"`
}

// ChangePassword replaces the signed-in User's password.
type ChangePassword struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// TOTPOffer is a TOTP secret to scan, in force once confirmed.
type TOTPOffer struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

// TOTPCode proves a TOTP code.
type TOTPCode struct {
	Code string `json:"code"`
}

// NewDevice creates a Device from a public key made in the browser; the
// private key never leaves it (ADR 0003).
type NewDevice struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

// DeviceInfo is a Device as its owner and Admins see it.
type DeviceInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Address   string    `json:"address"`
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"revoked"`
	// State is the effective state: active, blocked or throttled.
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// RecentBytes is what the Device moved in this hour and the last: the
	// "used in the last hour" mark. Never connection times (ADR 0003).
	RecentBytes uint64 `json:"recent_bytes"`
}

// DeviceUsage is a Device's usage at each resolution ADR 0003 keeps.
type DeviceUsage struct {
	Hourly  []UsageRow `json:"hourly"`  // the last 7 days, per Exit
	Daily   []UsageRow `json:"daily"`   // the last 90 days, per Exit
	Monthly []UsageRow `json:"monthly"` // every month, all Exits together
}

// UsageRow is bytes in one period through one Exit.
type UsageRow struct {
	Period string `json:"period"` // an RFC 3339 hour, a YYYY-MM-DD day, or a YYYY-MM month
	Exit   string `json:"exit,omitempty"`
	Up     uint64 `json:"up"`
	Down   uint64 `json:"down"`
}

// IssuedDevice is a new Device and everything its configs need, except the
// private key, which only the browser has.
type IssuedDevice struct {
	Device DeviceInfo   `json:"device"`
	Config DeviceConfig `json:"config"`
}

// DeviceConfig is the shared part of a Device's configs; each Endpoint is
// one config (ADR 0005).
type DeviceConfig struct {
	Address         string     `json:"address"`          // the Device's /32
	IPv6Placeholder string     `json:"ipv6_placeholder"` // a ULA /128, so apps drop IPv6 inside the tunnel
	DNS             string     `json:"dns"`
	BridgePublicKey string     `json:"bridge_public_key"`
	AllowedIPs      []string   `json:"allowed_ips"`
	Endpoints       []Endpoint `json:"endpoints"` // Main, Backup 1, Backup 2
}

// Endpoint is one config's Endpoint Slot.
type Endpoint struct {
	Label string `json:"label"`
	Host  string `json:"host"`
	Port  int    `json:"port"`
}

// CreateInvite asks for a new-User Invite.
type CreateInvite struct {
	Admin     bool `json:"admin,omitempty"`
	DeviceCap int  `json:"device_cap,omitempty"` // 0: the default of 3
}

// IssuedInvite is an Invite to hand over out of band.
type IssuedInvite struct {
	Token     string    `json:"token"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// UserInfo is a User as Admins see them.
type UserInfo struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Admin       bool   `json:"admin"`
	Suspended   bool   `json:"suspended"`
	TOTPEnabled bool   `json:"totp_enabled"`
	DeviceCap   int    `json:"device_cap"`
	DeviceCount int    `json:"device_count"`
	// MonthBytes is the User's total this calendar month: Admins see
	// totals per User, not per Device.
	MonthBytes uint64 `json:"month_bytes"`
}

// SetDeviceCap changes a User's Device cap.
type SetDeviceCap struct {
	DeviceCap int `json:"device_cap"`
}
