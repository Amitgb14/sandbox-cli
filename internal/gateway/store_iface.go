package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Store is everything the gateway keeps across a restart (sql_store.go: one
// implementation over SQLite or PostgreSQL). Every method takes the
// request's context, and every read returns an error: a database can fail,
// and a lookup that failed must not read as "not found" — a revoked key
// would pass, or a sandbox would seem to have no owner.
//
// Isolation between tenants is the gateway's: each method that reads a
// tenant's records is given the tenant, and the handlers decide whose they
// are. ForTenant adds a second wall where the database can hold one
// (PostgreSQL's row-level security): a query made through it sees only that
// tenant's rows whatever its own filter says.
type Store interface {
	// ForTenant is the store as tenant: on PostgreSQL every statement runs
	// under row-level security for tenant; on SQLite it is the store itself,
	// whose queries filter by tenant.
	ForTenant(tenant string) Store
	// Now is the store's clock, for records it stamps.
	Now() time.Time
	Close() error

	// --- API keys and accounts (sql_keys.go, sql_accounts.go) ---

	CreateKey(ctx context.Context, user, tenant string, scopes []string) (string, Key, error)
	CreateKeyExpiring(ctx context.Context, user, tenant string, scopes []string, expires *time.Time) (string, Key, error)
	// KeyBySecret is the active key a presented secret names; a sign-in's
	// key also needs its account active. ok is false for none.
	KeyBySecret(ctx context.Context, secret string) (Key, bool, error)
	RevokeKey(ctx context.Context, id string) error
	SetKeyExpiry(ctx context.Context, id string, at *time.Time) (Key, error)
	// SuspendKey revokes a key restorably until until; RestoreKey takes it
	// back (keys_suspend.go).
	SuspendKey(ctx context.Context, id string, until time.Time) (Key, error)
	RestoreKey(ctx context.Context, id string) (Key, error)
	Keys(ctx context.Context) ([]Key, error)
	// KeysOfUser is every key whose user is named user, in any tenant.
	KeysOfUser(ctx context.Context, user string) ([]Key, error)
	// StatCounts is the gateway's totals for the super admin's dashboard
	// (stats.go): counts and sums, never which or whose.
	StatCounts(ctx context.Context) (statCounts, error)
	// CreatePersonalKey makes a key for the user of key by, under by's
	// account or itself, with scopes it holds, ending no later than it,
	// acting only in org (their own tenant or one they are a member of);
	// PersonalKeys lists those made under the same root in org, and
	// RevokePersonalKey revokes one of them (keys_personal.go).
	CreatePersonalKey(ctx context.Context, by, org, name string, scopes []string, expires time.Time) (string, Key, error)
	PersonalKeys(ctx context.Context, by, org string) ([]Key, error)
	RevokePersonalKey(ctx context.Context, by, id string) (Key, error)

	CreateInvite(ctx context.Context, req api.CreateInviteRequest) (string, inviteRecord, error)
	CreateResetInvite(ctx context.Context, id string, valid time.Duration) (string, inviteRecord, error)
	RenewInvite(ctx context.Context, id string, valid time.Duration) (string, inviteRecord, error)
	Invites(ctx context.Context) ([]inviteRecord, error)
	RevokeInvite(ctx context.Context, id string) error
	InviteByToken(ctx context.Context, token string) (inviteRecord, bool, error)
	RedeemInvite(ctx context.Context, token, passwordHash string) (Key, error)
	Account(ctx context.Context, email string) (Key, bool, error)
	// AccountOf is the account a key is, or signs in to; ok is false for
	// a key of no account, and for a personal key.
	AccountOf(ctx context.Context, keyID string) (Key, bool, error)
	SignIn(ctx context.Context, account string, ttl time.Duration) (string, Key, error)
	SignOut(ctx context.Context, id string) error
	SetPassword(ctx context.Context, account, passwordHash, keep string) error

	// --- SSH (sql_ssh.go) ---

	AddSSHKey(ctx context.Context, user, tenant, sandbox, authorizedKey string) (SSHKey, error)
	RemoveSSHKey(ctx context.Context, id string) error
	SSHKeysByFingerprint(ctx context.Context, fingerprint string) ([]SSHKey, error)
	SSHKeysFor(ctx context.Context, user string) ([]SSHKey, error)
	NewSSHToken(ctx context.Context, user, tenant, sandbox string, ttl time.Duration) (string, time.Time, error)
	RedeemSSHToken(ctx context.Context, token string) (SSHToken, bool, error)

	// --- sandboxes, volumes, snapshots (sql_sandboxes.go) ---

	// RecordSandbox records a new sandbox's owner and size, and its details
	// as the node answered for it.
	RecordSandbox(ctx context.Context, id string, o Owner, size api.NodeResources, sb *api.Sandbox) error
	// UpdateSandboxDetails keeps a live sandbox's stored details in step
	// with its node's; a terminated one is marked so and stops counting.
	UpdateSandboxDetails(ctx context.Context, sbs ...api.Sandbox) error
	OwnerOf(ctx context.Context, sandbox string) (Owner, bool, error)
	SizeOf(ctx context.Context, sandbox string) (api.NodeResources, bool, error)
	// ForgetSandbox ends a sandbox's ownership: it is terminated, and its
	// row is kept as history until the history sweep.
	ForgetSandbox(ctx context.Context, sandbox string) error
	ForgetSandboxes(ctx context.Context, ids []string, reason string) error
	UsageOf(ctx context.Context, tenant string) (Usage, error)
	// LiveSandboxes is every sandbox not terminated, for what is reckoned
	// across nodes (lost.go).
	LiveSandboxes(ctx context.Context) ([]SandboxRow, error)
	SandboxesOn(ctx context.Context, node string) (map[string]time.Time, error)
	// SandboxHistory lists stored sandboxes, terminated ones included, a
	// tenant's ("" with all for an admin), newest first.
	SandboxHistory(ctx context.Context, f SandboxFilter) ([]SandboxRow, error)
	// PruneSandboxHistory deletes terminated sandboxes' rows older than
	// before, returning how many.
	PruneSandboxHistory(ctx context.Context, before time.Time) (int, error)

	SetVolume(ctx context.Context, name string, o Owner) error
	VolumeOwner(ctx context.Context, name string) (Owner, bool, error)
	ForgetVolume(ctx context.Context, name string) error
	VolumesOf(ctx context.Context, user, tenant string) (map[string]string, error)
	SetSnapshot(ctx context.Context, id string, o Owner) error
	SnapshotOwner(ctx context.Context, id string) (Owner, bool, error)
	ForgetSnapshotsGoneFrom(ctx context.Context, node string, holds map[string]bool) error
	ForgetSnapshot(ctx context.Context, id string) error

	// --- nodes (sql_nodes.go) ---

	PutNode(ctx context.Context, n NodeConfig) error
	RemoveNode(ctx context.Context, name string) error
	Nodes(ctx context.Context) ([]NodeConfig, error)

	// --- organisations (sql_orgs.go) ---

	CreateOrg(ctx context.Context, name, user, userTenant string, maxOwned int) (orgRecord, error)
	// Org is the organisation name names, matched without regard to case.
	Org(ctx context.Context, name string) (orgRecord, bool, error)
	Orgs(ctx context.Context) ([]orgRecord, error)
	MemberRole(ctx context.Context, org, user, userTenant string) (string, bool, error)
	MembershipsOf(ctx context.Context, user, userTenant string) ([]orgMembership, error)
	SetMember(ctx context.Context, org, user, userTenant, role string) (string, error)
	RemoveMember(ctx context.Context, org, user, userTenant string) error

	// --- policies (sql_policies.go) ---

	PutPolicy(ctx context.Context, p api.UserPolicy) error
	DeletePolicy(ctx context.Context, name string) error
	Policies(ctx context.Context) ([]api.UserPolicy, error)
	AssignPolicy(ctx context.Context, user, tenant, name string) error
	PolicyOf(ctx context.Context, user, keyTenant string) (api.UserPolicy, bool, error)
	PolicyActingIn(ctx context.Context, user, tenant string) (api.UserPolicy, bool, error)

	// --- allowlist groups (egress_groups.go) ---

	PutEgressGroup(ctx context.Context, g api.EgressGroup) error
	DeleteEgressGroup(ctx context.Context, name string) error
	EgressGroups(ctx context.Context) ([]api.EgressGroup, error)

	// --- jobs (sql_jobs.go) ---

	PutJob(ctx context.Context, r *jobRecord) error
	Job(ctx context.Context, id string) (*jobRecord, bool, error)
	// JobsOf lists one user's jobs in a tenant, oldest first.
	JobsOf(ctx context.Context, user, tenant string) ([]*jobRecord, error)
	AllJobs(ctx context.Context) ([]*jobRecord, error)
	// RunningJobs is the owner of every job running and not cancelled.
	RunningJobs(ctx context.Context) (map[string]Owner, error)
	// UpdateJob and UpdateRun change a job, or one of its runs, in one
	// read-modify-write.
	UpdateJob(ctx context.Context, id string, fn func(*jobRecord)) error
	UpdateRun(ctx context.Context, id string, n int, fn func(*api.JobRun)) (api.JobRun, error)
	ForgetJob(ctx context.Context, id string) error

	// --- secrets (sql_secrets.go): values arrive sealed and leave sealed ---

	PutSecret(ctx context.Context, tenant, name string, sealed []byte) error
	SealedSecret(ctx context.Context, tenant, name string) ([]byte, bool, error)
	SecretsOf(ctx context.Context, tenant string) ([]api.SecretInfo, error)
	DeleteSecret(ctx context.Context, tenant, name string) error

	// --- registry logins (registry_logins.go): the token arrives sealed and leaves sealed ---

	PutRegistryLogin(ctx context.Context, tenant, registry, username string, sealed []byte) error
	RegistryLogin(ctx context.Context, tenant, registry string) (username string, sealed []byte, ok bool, err error)
	RegistryLoginsOf(ctx context.Context, tenant string) ([]api.RegistryLoginInfo, error)
	DeleteRegistryLogin(ctx context.Context, tenant, registry string) error

	// --- agent logins (agent_logins.go): a user's, sealed; never a tenant's ---

	UserOfKey(ctx context.Context, keyID string) (userID string, ok bool, err error)
	PutAgentLogin(ctx context.Context, userID, agent string, sealed []byte) error
	AgentLogin(ctx context.Context, userID, agent string) (sealed []byte, ok bool, err error)
	AgentLoginsOf(ctx context.Context, userID string) ([]agentLoginRow, error)
	DeleteAgentLogin(ctx context.Context, userID, agent string) error
	KeepAgentLogin(ctx context.Context, k agentLoginKeep) error
	AgentLoginKeeps(ctx context.Context) ([]agentLoginKeep, error)
	AgentLoginKeep(ctx context.Context, sandbox string) (agentLoginKeep, bool, error)
	SetAgentLoginSeen(ctx context.Context, sandbox, seen string) error
	DropAgentLoginKeep(ctx context.Context, sandbox string) error

	// --- services (sql_services.go) ---

	Services(ctx context.Context) ([]serviceRecord, error)
	PutService(ctx context.Context, r serviceRecord) error
	PutServiceRetiring(ctx context.Context, r serviceRecord, retire []string) error
	DeleteService(ctx context.Context, tenant, name string, retire []string) error
	Retire(ctx context.Context, ids ...string) error
	Retiring(ctx context.Context) ([]string, error)
	Retired(ctx context.Context, ids ...string) error
}

// SandboxRow is a sandbox as the store keeps it: owner, size and details.
type SandboxRow struct {
	ID       string
	Owner    Owner
	CPUs     float64
	MemoryMB int
	DiskMB   int
	Recorded time.Time
	// Details is the sandbox as its node last answered for it; nil for a
	// row recorded before details were kept.
	Details *api.Sandbox
	// TerminatedAt is set once it is gone, with why.
	TerminatedAt     *time.Time
	TerminatedReason string
}

// SandboxFilter narrows SandboxHistory.
type SandboxFilter struct {
	Tenant    string
	AllTenant bool // every tenant's: an admin's view
	User      string
	Live      bool // not terminated
	Limit     int
}

var _ Store = (*sqlStore)(nil)

// ErrStore wraps a failure of the store itself, as opposed to a refusal: a
// handler answers it 503, never as "not found".
var ErrStore = errors.New("the gateway's database did not answer")

// storeFor is the store as p may see it: p's tenant's rows only, which
// PostgreSQL holds it to with row-level security (ForTenant). An admin acts
// across tenants, and gets the whole store.
func (g *Gateway) storeFor(p Principal) Store {
	if p.Can(ScopeAdmin) {
		return g.store
	}
	return g.store.ForTenant(p.Tenant)
}

// writeStoreErr answers a request whose store call failed: 503 when the
// database did not answer — the client may try again — and 500 otherwise.
// The cause is logged, never sent.
func (g *Gateway) writeStoreErr(w http.ResponseWriter, err error) {
	g.logf("store: %v", err)
	if errors.Is(err, ErrStore) || errors.Is(err, context.DeadlineExceeded) {
		writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, ErrStore.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the gateway failed to read or write its state")
}
