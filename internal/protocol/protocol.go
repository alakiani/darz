// Package protocol holds the JSON messages the Coordinator and the Agent
// exchange under /v1 (ADR 0002), and the Admin API messages for enrolling
// Nodes.
package protocol

import "time"

// Role is what a Node does.
type Role string

const (
	RoleBridge Role = "bridge"
	RoleExit   Role = "exit"
)

// Valid reports whether r is a known Role.
func (r Role) Valid() bool { return r == RoleBridge || r == RoleExit }

// Snapshot is one version of a Node's complete desired state, as served by
// GET /v1/state.
type Snapshot struct {
	Version uint64 `json:"version"`
	State   State  `json:"state"`
}

// State is a Node's complete desired state. A Node sees only what its Roles
// need. Later sessions add Links, Devices, Rules and the rest.
type State struct {
	Node NodeInfo `json:"node"`
}

// NodeInfo is what a Node is told about itself.
type NodeInfo struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Roles []Role   `json:"roles"`
	Tags  []string `json:"tags"`
}

// Health is what an Agent POSTs to /v1/health every 30 s, and to confirm a
// freshly applied snapshot.
type Health struct {
	AppliedVersion uint64    `json:"applied_version"`
	AgentVersion   string    `json:"agent_version"`
	Time           time.Time `json:"time"` // the Agent's clock
	Addr           string    `json:"addr"` // the source address the Agent dialled from
	// ApplyFailed lists snapshot versions the Agent applied and then reverted
	// because it lost the Coordinator within the confirm window.
	ApplyFailed []uint64 `json:"apply_failed,omitempty"`
}

// EnrollRequest is POSTed to /v1/enroll by a new Agent.
type EnrollRequest struct {
	Token string `json:"token"`
	CSR   string `json:"csr"` // PEM
}

// EnrollResponse carries the Agent's client certificate.
type EnrollResponse struct {
	NodeID      string `json:"node_id"`
	Certificate string `json:"certificate"` // PEM
}

// CreateNodeRequest is POSTed to /v1/nodes with a nodes:enroll API token.
type CreateNodeRequest struct {
	Name  string   `json:"name"`
	Roles []Role   `json:"roles"`
	Tags  []string `json:"tags"`
	// Reenroll mints a new enrollment token for a Node that already enrolled,
	// so it can re-key.
	Reenroll bool `json:"reenroll,omitempty"`
}

// CreateNodeResponse reports the Node and, unless it already enrolled and no
// re-enrollment was asked for, a fresh one-time enrollment token.
type CreateNodeResponse struct {
	Node            NodeStatus `json:"node"`
	EnrollmentToken string     `json:"enrollment_token,omitempty"`
	TokenExpiresAt  *time.Time `json:"token_expires_at,omitempty"`
}

// NodeStatus is a Node as the Admin API reports it, from GET /v1/nodes/{id}.
type NodeStatus struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Roles    []Role   `json:"roles"`
	Tags     []string `json:"tags"`
	Enrolled bool     `json:"enrolled"`
	// LastHealth is the latest health report, absent until the first one.
	LastHealth *HealthStatus `json:"last_health,omitempty"`
}

// HealthStatus is the latest health report as the Coordinator stored it.
type HealthStatus struct {
	Health
	ReceivedAt time.Time `json:"received_at"`
	// SeenAddr is the source address the Coordinator saw. A mismatch with
	// Health.Addr is shown to the Admin.
	SeenAddr string `json:"seen_addr"`
}

// Error is the body of every non-2xx response.
type Error struct {
	Error string `json:"error"`
}
