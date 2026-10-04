// Package gateway is sandbox-gateway: one address in front of any number of
// sandboxd nodes (docs/roadmap/task-7-fleet-gateway.md).
//
// It speaks the same Sandbox API v1 as a node, so every client — the CLI's
// contexts, the SDKs, Studio, the conformance suite — works against it with
// only its address and credential changed. What it adds is what one machine
// never needed: users with API keys, ownership of every sandbox, quotas, a
// scheduler that picks a node, routing by an id that names its node, and an
// SSH server on one port for every sandbox.
//
// The rules that keep it safe are the node's, moved up one layer:
//   - Nodes are reached only by the gateway. A user never holds a node token.
//   - Every call is authorised against the sandbox's recorded owner before it
//     is forwarded. The id names a node only to route; ownership comes from
//     the store, never from the id or from a label a request set.
//   - A request may tighten what the gateway's policy allows, never loosen it.
//     `owner` and `tenant` labels are the gateway's to set, and a request that
//     sets them is refused.
//   - Secrets travel by reference. An API key or an SSH token is stored only
//     as a hash and appears in a log or the audit record only by its id.
//
// This file is the contract the pieces are built against: the store (state
// that must survive a restart), the principal a credential becomes, and the
// router that turns "this principal, this sandbox, this scope" into a client
// for the right node.
package gateway

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Scopes an API key may carry. A key holds only the scopes it was issued
// with; ScopeAdmin is the operator's, for keys and nodes.
const (
	ScopeRead   = "sandbox:read"   // get, list, logs, events, files read
	ScopeCreate = "sandbox:create" // create, and act on what this key's user owns
	ScopeDelete = "sandbox:delete" // terminate
	ScopeSSH    = "sandbox:ssh"    // SSH sessions, and SSH keys and tokens for them
	ScopeAdmin  = "admin"          // users, keys, nodes, cordon; every sandbox
	// ScopeSecretsWrite sets and removes the tenant's secrets (secrets.go).
	// Listing their names needs only ScopeRead; a value is never returned.
	ScopeSecretsWrite = "secrets:write"
	// ScopeOrgCreate creates organisations (orgs.go), each a tenant of its
	// own with its own quota; how many one user may make is capped.
	ScopeOrgCreate = "org:create"
)

// AllScopes lists the scopes, for help text and validation.
var AllScopes = []string{ScopeRead, ScopeCreate, ScopeDelete, ScopeSSH, ScopeAdmin, ScopeSecretsWrite, ScopeOrgCreate}

// Labels the gateway stamps on every sandbox it creates. A request that sets
// either is refused: they decide who may act on the sandbox.
const (
	LabelOwner  = "gateway.owner"
	LabelTenant = "gateway.tenant"
)

// Principal is who a credential says the caller is, once checked.
type Principal struct {
	User string
	// Tenant is the tenant the request acts in: the key's own, or the
	// organisation X-Sandbox-Org selected, checked against the user's
	// memberships once, as the request is authenticated (orgs.go). Every
	// handler keys isolation on it.
	Tenant string
	// KeyTenant is the tenant of the credential itself, whatever was
	// selected: who the user is, since a user name is unique only within a
	// tenant. Empty for a principal not made from a key.
	KeyTenant string
	// KeyID names the credential in logs and the audit record; never the
	// secret itself.
	KeyID  string
	Scopes []string
	// Sandbox, when set, confines the principal to that one sandbox: what a
	// short-lived SSH token becomes.
	Sandbox string
}

// Can reports whether p holds scope. Admin holds every scope.
func (p Principal) Can(scope string) bool {
	return slices.Contains(p.Scopes, ScopeAdmin) || slices.Contains(p.Scopes, scope)
}

// Key is an API key as the store keeps it: its hash, never its secret.
type Key struct {
	ID      string    `json:"id"`
	User    string    `json:"user"`
	Tenant  string    `json:"tenant"`
	Scopes  []string  `json:"scopes"`
	Hash    string    `json:"hash"` // hex SHA-256 of the secret
	Created time.Time `json:"created"`
	Revoked bool      `json:"revoked,omitempty"`
}

// SSHKey is a public key a user may log in with.
type SSHKey struct {
	ID          string `json:"id"`
	User        string `json:"user"`
	Tenant      string `json:"tenant"`
	Fingerprint string `json:"fingerprint"` // SHA256:… as ssh-keygen prints it
	// AuthorizedKey is the key as one authorized_keys line, with no options.
	AuthorizedKey string `json:"authorized_key"`
	// Sandbox, when set, limits the key to that sandbox; empty is every
	// sandbox the user owns.
	Sandbox string    `json:"sandbox,omitempty"`
	Created time.Time `json:"created"`
}

// SSHToken is a redeemed short-lived SSH token: the SSH username that is
// itself the credential, for one sandbox, until it expires.
type SSHToken struct {
	User    string
	Tenant  string
	Sandbox string
	Expires time.Time
}

// Owner is who a sandbox belongs to.
type Owner struct {
	User   string `json:"user"`
	Tenant string `json:"tenant"`
	Node   string `json:"node"`
}

// NodeConfig is how the gateway reaches one sandboxd.
type NodeConfig struct {
	// Name is the node's id, as it appears in its sandboxes' ids.
	Name string `json:"name"`
	// Endpoint is https://host:port, or unix:///path for a node on the same
	// machine.
	Endpoint string `json:"endpoint"`
	// TokenFile holds the node's bearer token; CAFile the CA that signed the
	// node's certificate; CertFile and KeyFile the gateway's client
	// certificate for mutual TLS.
	TokenFile string `json:"token_file,omitempty"`
	CAFile    string `json:"ca_file,omitempty"`
	CertFile  string `json:"cert_file,omitempty"`
	KeyFile   string `json:"key_file,omitempty"`
}

// Store is everything the gateway must keep across a restart. Placement can
// be rebuilt from the nodes' own listings; ownership cannot, which is why it
// lives here.
type Store interface {
	CreateKey(user, tenant string, scopes []string) (secret string, k Key, err error)
	// KeyBySecret checks a presented secret against the stored hashes, in
	// constant time, and returns the key it matches if not revoked.
	KeyBySecret(secret string) (Key, bool)
	RevokeKey(id string) error
	Keys() []Key

	AddSSHKey(user, tenant, sandbox, authorizedKey string) (SSHKey, error)
	RemoveSSHKey(id string) error
	// SSHKeysByFingerprint returns every stored key with that fingerprint.
	SSHKeysByFingerprint(fingerprint string) []SSHKey
	SSHKeysFor(user string) []SSHKey

	// NewSSHToken issues a token for one sandbox; only its hash is stored.
	NewSSHToken(user, tenant, sandbox string, ttl time.Duration) (token string, expires time.Time, err error)
	RedeemSSHToken(token string) (SSHToken, bool)

	SetOwner(sandbox string, o Owner) error
	OwnerOf(sandbox string) (Owner, bool)
	ForgetSandbox(sandbox string) error

	PutNode(n NodeConfig) error
	RemoveNode(name string) error
	Nodes() []NodeConfig

	// MemberRole returns the role user — named with the tenant of their own
	// keys — holds in organisation org, and false when not a member.
	MemberRole(org, user, userTenant string) (string, bool)
}

// Router turns a principal, a sandbox reference and a scope into a client for
// the node that holds the sandbox, or an error saying why not. It is the one
// place authorisation happens, so the HTTP front and the SSH front cannot
// disagree about who may do what.
type Router interface {
	// Resolve checks that p holds scope and may act on ref (an id, or a name
	// among p's own sandboxes), and returns the sandbox's id and a client for
	// its node.
	Resolve(ctx context.Context, p Principal, ref, scope string) (id string, c *api.Client, err error)
}

// Errors the router returns, mapped to API error codes by the HTTP front and
// to a refused session by the SSH front.
var (
	ErrUnauthenticated = errors.New("no valid credential")
	ErrForbidden       = errors.New("this credential may not do that")
	ErrNotFound        = errors.New("no such sandbox")
	ErrNodeDown        = errors.New("the node holding this sandbox is not answering")
)
