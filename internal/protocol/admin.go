package protocol

import "time"

// Messages of the admin panel's API (the panel prototype, issue #14).

// Overview is the status board.
type Overview struct {
	Nodes []NodeTile `json:"nodes"`
	Links []LinkTile `json:"links"`
	Sets  []SetInfo  `json:"sets"`
	// EndpointSlots are the slots and the probe's view of each domestic
	// Bridge (ADR 0005).
	EndpointSlots SlotStatus `json:"endpoint_slots"`
	// Backup is the nightly backup's state (ADR 0001).
	Backup BackupStatus `json:"backup"`
	// UndeliveredAlerts are alerts no Exit could relay to Telegram yet,
	// shown as a banner (ADR 0006).
	UndeliveredAlerts []UndeliveredAlert `json:"undelivered_alerts"`
}

// BackupStatus is the nightly backup on the status board.
type BackupStatus struct {
	// Configured is false until the Coordinator has an age recipient.
	Configured  bool       `json:"configured"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastError   string     `json:"last_error,omitempty"` // of the last attempt, if it failed
	SizeBytes   int64      `json:"size_bytes"`           // of the last good backup, encrypted
}

// UndeliveredAlert is an alert waiting for a healthy Exit.
type UndeliveredAlert struct {
	ID         uint64    `json:"id"`
	Text       string    `json:"text"`
	ReceivedAt time.Time `json:"received_at"`
	Attempts   int       `json:"attempts"`
	LastError  string    `json:"last_error"`
}

// NodeTile is a Node on the status board.
type NodeTile struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Roles    []Role   `json:"roles"`
	Tags     []string `json:"tags"`
	Enrolled bool     `json:"enrolled"`
	// LastContact is the Agent's latest health report, if any.
	LastContact    *time.Time `json:"last_contact,omitempty"`
	AppliedVersion uint64     `json:"applied_version"`
	LatestVersion  uint64     `json:"latest_version"`
	AgentVersion   string     `json:"agent_version,omitempty"`
	// Addr is what the Agent says it holds, SeenAddr what the Coordinator
	// sees; a mismatch is shown (ADR 0002).
	Addr     string `json:"addr,omitempty"`
	SeenAddr string `json:"seen_addr,omitempty"`
}

// LinkTile is a Link on the status board and the Links screen.
type LinkTile struct {
	ID        uint64         `json:"id"`
	A         string         `json:"a"` // Node names
	B         string         `json:"b"`
	Crosses   bool           `json:"crosses"` // the Link crosses the filter
	UpA       bool           `json:"up_a"`    // as each end reports it
	UpB       bool           `json:"up_b"`
	Transport Transport      `json:"transport,omitempty"`
	RTTMillis float64        `json:"rtt_ms"`
	Instances []InstanceTile `json:"instances"`
}

// InstanceTile is one candidate instance of a Link.
type InstanceTile struct {
	ID         uint64    `json:"id"`
	Transport  Transport `json:"transport"`
	ListenPort uint16    `json:"listen_port"`
	Up         bool      `json:"up"` // as the initiating end reports it
}

// SetInfo is a Destination Set with its feed's state.
type SetInfo struct {
	ID          uint64     `json:"id"`
	Name        string     `json:"name"`
	Feed        string     `json:"feed,omitempty"`
	Prefixes    int        `json:"prefixes"`
	Addresses   uint64     `json:"addresses"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	CanRollBack bool       `json:"can_roll_back"`
	Additions   []string   `json:"additions"`
	Removals    []string   `json:"removals"`
}

// Overrides are an Admin's additions to and removals from a set.
type Overrides struct {
	Additions []string `json:"additions"`
	Removals  []string `json:"removals"`
}

// RuleInfo is a Rule as the panel shows it.
type RuleInfo struct {
	ID           uint64   `json:"id"`
	Name         string   `json:"name"`
	Priority     int      `json:"priority"`
	Set          string   `json:"set"`
	RequiredTags []string `json:"required_tags"`
	Fallback     Fallback `json:"fallback"`
	Builtin      bool     `json:"builtin"` // the default Rule: locked
}

// RuleEdit changes the IR Rule's Tags and fallback.
type RuleEdit struct {
	RequiredTags []string `json:"required_tags"`
	Fallback     Fallback `json:"fallback"`
}

// InviteRecord is an Invite in the Admin's list.
type InviteRecord struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Handle    string    `json:"handle,omitempty"`
	Admin     bool      `json:"admin"`
	DeviceCap int       `json:"device_cap,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// Status is "open", "used", "expired" or "withdrawn".
	Status string `json:"status"`
}

// NewNode adds a Node from the panel. SSHHost is only used to write the
// enroll command; WAYS learns the Node's address from its Agent.
type NewNode struct {
	Name     string   `json:"name"`
	Roles    []Role   `json:"roles"`
	Tags     []string `json:"tags"`
	SSHHost  string   `json:"ssh_host"`
	Reenroll bool     `json:"reenroll,omitempty"`
}

// EnrollCommand is the Ansible command that enrolls a Node, with its
// one-time token (ADR 0002).
type EnrollCommand struct {
	Node      NodeStatus `json:"node"`
	Command   string     `json:"command,omitempty"` // empty when already enrolled
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// NodeEdit replaces a Node's Roles and Tags.
type NodeEdit struct {
	Roles []Role   `json:"roles"`
	Tags  []string `json:"tags"`
}

// NodeDetail is a Node with its Links and, for a Bridge, how it carries
// each Rule.
type NodeDetail struct {
	Node  NodeTile   `json:"node"`
	Links []LinkTile `json:"links"`
	Paths []PathInfo `json:"paths"`
}

// PathInfo is how a Bridge carries one Rule (ADR 0008).
type PathInfo struct {
	Rule     string  `json:"rule"`
	Mode     string  `json:"mode"`
	Eligible int     `json:"eligible"`
	Shares   []Share `json:"shares"`
}

// Share is one candidate's buckets. Candidate names the Node a Link leads
// to, "local", or "drop".
type Share struct {
	Candidate string `json:"candidate"`
	Transit   bool   `json:"transit"` // through another Bridge
	Buckets   int    `json:"buckets"`
}

// TransportList is the global ordered candidate Transport list (ADR 0007).
type TransportList struct {
	Enabled []Transport `json:"enabled"`
	Known   []Transport `json:"known"`
}

// TokenInfo is an API token; its secret is shown only when it is made.
type TokenInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
}

// NewToken makes a nodes:enroll token.
type NewToken struct {
	Name string `json:"name"`
}

// IssuedToken carries the secret once.
type IssuedToken struct {
	Token  TokenInfo `json:"token"`
	Secret string    `json:"secret"`
}

// SlotStatus is where the Endpoint Slots point and why.
type SlotStatus struct {
	Slots   []SlotTile   `json:"slots"`
	Bridges []SlotBridge `json:"bridges"`
}

// SlotTile is one Endpoint Slot.
type SlotTile struct {
	Name   string `json:"name"`             // such as a.4miz.ir
	Bridge string `json:"bridge,omitempty"` // the Bridge's name; absent until one was eligible
	// Addr is what the DNS record holds; empty while an update is due.
	Addr     string     `json:"addr,omitempty"`
	Since    *time.Time `json:"since,omitempty"` // when it last moved
	Repoints int        `json:"repoints"`
}

// SlotBridge is one domestic Bridge as slot eligibility judges it.
type SlotBridge struct {
	Name     string `json:"name"`
	Eligible bool   `json:"eligible"`
	// Reasons say why it is not eligible.
	Reasons []string     `json:"reasons,omitempty"`
	Probe   *ProbeResult `json:"probe,omitempty"`
	// IneligibleSince is when it last became ineligible; its slots move
	// 60 s later.
	IneligibleSince *time.Time `json:"ineligible_since,omitempty"`
}

// SlotRebalance is a plan that spreads the Endpoint Slots over the
// eligible Bridges with the fewest moves, or what applying one moved.
type SlotRebalance struct {
	Moves []SlotMove `json:"moves"`
	// Note says what the moves mean for Users.
	Note string `json:"note"`
}

// SlotMove is one Endpoint Slot moving to another Bridge.
type SlotMove struct {
	Slot string `json:"slot"`           // such as a.4miz.ir
	From string `json:"from,omitempty"` // the Bridge's name; absent for an unassigned slot
	To   string `json:"to"`
}

// ProbeResult is the probe Device's latest check of a Bridge.
type ProbeResult struct {
	OK    bool      `json:"ok"`
	At    time.Time `json:"at"`
	Error string    `json:"error,omitempty"`
}
