package gateway

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Accounts: people who sign in with an email address and a password.
//
// An API key is a fine credential for a program and a poor one for a
// person: it has to be sent to them, and wherever it was sent — an email, a
// chat — it keeps working until it is revoked. An account changes only how
// the person proves who they are:
//
//   - An admin invites an email address with the user, tenant, scopes,
//     policy and lifetime a key would have. The invite is a link holding a
//     one-time token that lapses in days, so an email that leaks after it
//     was used, or after it lapsed, gives nothing away.
//   - Opening it, the person sets a password, and the gateway makes the
//     account: a key, with the email and the password's hash on it, whose
//     own secret nobody is ever shown. Everything that holds a key to
//     account — ownership, policies, expiry, revocation, a job running in
//     the user's name while they are away — holds the account the same way.
//   - Signing in with the password mints a key for that sign-in alone
//     (Key.Parent), short-lived, ended by signing out and never outliving
//     the account. Hosted Studio exchanges it for a session as it does an
//     invite link's key.
//
// A forgotten password is reset by the admin sending a new invite for the
// account; there is no reset by email the gateway would have to send.
//
// The sign-in endpoints take no key, so they are the gateway's only routes
// open to anyone who can reach it. Each guess costs a PBKDF2 hash, a few at
// a time; an address that keeps failing is locked for a while, and failures
// gateway-wide are rate-limited, so neither one address nor many can be
// tried quickly.

// inviteRecord is an invite as the store keeps it: its token's hash.
type inviteRecord struct {
	ID            string    `json:"id"`
	Hash          string    `json:"hash"`
	Email         string    `json:"email"`
	User          string    `json:"user"`
	Tenant        string    `json:"tenant,omitempty"`
	Scopes        []string  `json:"scopes"`
	Policy        string    `json:"policy,omitempty"`
	ExpiresInSecs int       `json:"expires_in_secs,omitempty"`
	Key           string    `json:"key,omitempty"` // a reset: the account it sets the password of
	Created       time.Time `json:"created"`
	Expires       time.Time `json:"expires"`
}

func (iv inviteRecord) info() api.InviteInfo {
	return api.InviteInfo{ID: iv.ID, Email: iv.Email, User: iv.User, Tenant: iv.Tenant, Scopes: slices.Clone(iv.Scopes),
		Policy: iv.Policy, ExpiresInSecs: iv.ExpiresInSecs, Reset: iv.Key != "", Created: iv.Created, Expires: iv.Expires}
}

const (
	inviteSecretPrefix = "sgi_"
	inviteValid        = 48 * time.Hour
	inviteMaxValid     = 7 * 24 * time.Hour
	loginTTL           = 12 * time.Hour
	loginMaxTTL        = 7 * 24 * time.Hour
	passwordMin        = 10
	passwordMax        = 256
)

// Errors of the account store.
var (
	ErrEmailTaken     = errors.New("an account with this email exists; send it a password reset instead")
	ErrUserHasAccount = errors.New("this user already has an account")
	ErrNoSuchInvite   = errors.New("this invite is not valid: used, withdrawn or expired")
	ErrNotAccount     = errors.New("this key is not an account")
)

var emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalEmail is email as accounts are matched on: trimmed, lowercase. ok is
// false for one that is not an address.
func NormalEmail(email string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(email))
	return e, len(e) <= 254 && emailRE.MatchString(e)
}

// --- passwords ------------------------------------------------------------------

// passwordIterations is PBKDF2-SHA256's work factor, as recommended for it
// at the time of writing; tests lower it. The count is kept in each hash,
// so raising it later leaves existing passwords working.
var passwordIterations = 600_000

// hashing bounds how many hashes run at once: each is a few hundred
// milliseconds of one CPU, and a burst of sign-ins must not take the
// gateway's CPUs from everything else it does.
var hashing = make(chan struct{}, 2)

func checkPassword(pw string) error {
	switch {
	case len(pw) < passwordMin:
		return fmt.Errorf("a password is at least %d characters", passwordMin)
	case len(pw) > passwordMax:
		return fmt.Errorf("a password is at most %d characters", passwordMax)
	}
	return nil
}

func hashPassword(pw string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	hashing <- struct{}{}
	dk, err := pbkdf2.Key(sha256.New, pw, salt, passwordIterations, 32)
	<-hashing
	if err != nil {
		panic(err) // only for parameters out of FIPS bounds, which these are not
	}
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations, b64.EncodeToString(salt), b64.EncodeToString(dk))
}

// dummyHash is what a sign-in for an unknown address is checked against,
// so it takes as long as one for an address that exists.
var dummyHash = sync.OnceValue(func() string { return hashPassword("not a password anyone has") })

func verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[2])
	want, err2 := b64.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	hashing <- struct{}{}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	<-hashing
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// --- the store ------------------------------------------------------------------

// pruneInvitesLocked forgets invites past their expiry. Under mu.
func (s *FileStore) pruneInvitesLocked(now time.Time) {
	s.st.Invites = slices.DeleteFunc(s.st.Invites, func(iv inviteRecord) bool { return !now.Before(iv.Expires) })
}

// accountIndex is the index of the live account for email, -1 for none.
// Under mu.
func (s *FileStore) accountIndex(email string) int {
	return slices.IndexFunc(s.st.Keys, func(k Key) bool { return k.Email == email && !k.Revoked })
}

// CreateInvite records an invite and returns its token, shown once. A new
// invite for an address replaces one still pending for it, so only the
// newest link works.
func (s *FileStore) CreateInvite(req api.CreateInviteRequest) (string, inviteRecord, error) {
	email, ok := NormalEmail(req.Email)
	switch {
	case !ok:
		return "", inviteRecord{}, fmt.Errorf("email %q: not an address", req.Email)
	case !ValidUser(req.User):
		return "", inviteRecord{}, fmt.Errorf("user %q: letters, digits and . _ @ + -, at most 64", req.User)
	case req.Tenant != "" && !ValidUser(req.Tenant):
		return "", inviteRecord{}, fmt.Errorf("tenant %q: letters, digits and . _ @ + -, at most 64", req.Tenant)
	case req.ExpiresInSecs < 0:
		return "", inviteRecord{}, errors.New("expires_in_secs must not be negative")
	case req.ValidSecs < 0 || time.Duration(req.ValidSecs)*time.Second > inviteMaxValid:
		return "", inviteRecord{}, fmt.Errorf("valid_secs: at most %d", int(inviteMaxValid/time.Second))
	}
	if err := ValidateScopes(req.Scopes); err != nil {
		return "", inviteRecord{}, err
	}
	valid := inviteValid
	if req.ValidSecs > 0 {
		valid = time.Duration(req.ValidSecs) * time.Second
	}
	now := s.now().UTC()
	token := newSecret(inviteSecretPrefix)
	iv := inviteRecord{ID: newID("inv_"), Hash: hashSecret(token), Email: email, User: req.User, Tenant: req.Tenant,
		Scopes: slices.Clone(req.Scopes), Policy: req.Policy, ExpiresInSecs: req.ExpiresInSecs,
		Created: now, Expires: now.Add(valid)}
	err := s.change(func() error {
		if err := s.accountFreeLocked(email, req.User, req.Tenant); err != nil {
			return err
		}
		if req.Policy != "" && !slices.ContainsFunc(s.st.Policies, func(p api.UserPolicy) bool { return p.Name == req.Policy }) {
			return ErrNoSuchPolicy
		}
		s.pruneInvitesLocked(now)
		s.st.Invites = slices.DeleteFunc(s.st.Invites, func(o inviteRecord) bool { return o.Email == email && o.Key == "" })
		s.st.Invites = append(s.st.Invites, iv)
		return nil
	})
	if err != nil {
		return "", inviteRecord{}, err
	}
	return token, iv, nil
}

// accountFreeLocked refuses a new account whose address, or whose user,
// one has already. Under mu.
func (s *FileStore) accountFreeLocked(email, user, tenant string) error {
	if s.accountIndex(email) >= 0 {
		return ErrEmailTaken
	}
	if slices.ContainsFunc(s.st.Keys, func(k Key) bool {
		return k.Email != "" && !k.Revoked && k.User == user && k.Tenant == tenant
	}) {
		return ErrUserHasAccount
	}
	return s.nameTakenInOrg(tenant, user, tenant)
}

// CreateResetInvite records an invite that sets a new password for the
// account key id. A pending reset for it is replaced.
func (s *FileStore) CreateResetInvite(id string, valid time.Duration) (string, inviteRecord, error) {
	now := s.now().UTC()
	token := newSecret(inviteSecretPrefix)
	var iv inviteRecord
	err := s.change(func() error {
		i := s.keyIndex(id)
		switch {
		case i < 0:
			return ErrNoSuchKey
		case s.st.Keys[i].Email == "":
			return ErrNotAccount
		case s.st.Keys[i].Revoked:
			return ErrKeyRevoked
		}
		k := s.st.Keys[i]
		iv = inviteRecord{ID: newID("inv_"), Hash: hashSecret(token), Email: k.Email, User: k.User, Tenant: k.Tenant,
			Scopes: slices.Clone(k.Scopes), Key: k.ID, Created: now, Expires: now.Add(valid)}
		s.pruneInvitesLocked(now)
		s.st.Invites = slices.DeleteFunc(s.st.Invites, func(o inviteRecord) bool { return o.Key == id })
		s.st.Invites = append(s.st.Invites, iv)
		return nil
	})
	if err != nil {
		return "", inviteRecord{}, err
	}
	return token, iv, nil
}

// RenewInvite gives a pending invite a new token, valid for valid from now,
// for sending it again: the old link stops working, so only the newest one
// sent works, as when an address is invited again. What the invite is for
// is kept.
func (s *FileStore) RenewInvite(id string, valid time.Duration) (string, inviteRecord, error) {
	now := s.now().UTC()
	token := newSecret(inviteSecretPrefix)
	var out inviteRecord
	err := s.change(func() error {
		s.pruneInvitesLocked(now)
		i := slices.IndexFunc(s.st.Invites, func(iv inviteRecord) bool { return iv.ID == id })
		if i < 0 {
			return ErrNoSuchInvite
		}
		iv := &s.st.Invites[i]
		iv.Hash = hashSecret(token)
		iv.Expires = now.Add(valid)
		out = *iv
		out.Scopes = slices.Clone(iv.Scopes)
		return nil
	})
	if err != nil {
		return "", inviteRecord{}, err
	}
	return token, out, nil
}

// Invites lists the invites still usable, oldest first.
func (s *FileStore) Invites() []inviteRecord {
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []inviteRecord
	for _, iv := range s.st.Invites {
		if now.Before(iv.Expires) {
			iv.Scopes = slices.Clone(iv.Scopes)
			out = append(out, iv)
		}
	}
	return out
}

// RevokeInvite withdraws an invite.
func (s *FileStore) RevokeInvite(id string) error {
	return s.change(func() error {
		i := slices.IndexFunc(s.st.Invites, func(iv inviteRecord) bool { return iv.ID == id })
		if i < 0 {
			return ErrNoSuchInvite
		}
		s.st.Invites = slices.Delete(s.st.Invites, i, i+1)
		return nil
	})
}

// inviteIndex finds a usable invite by its token, in constant time over the
// hashes as KeyBySecret does. Under mu.
func (s *FileStore) inviteIndex(token string, now time.Time) int {
	if !strings.HasPrefix(token, inviteSecretPrefix) {
		return -1
	}
	h := []byte(hashSecret(token))
	found := -1
	for i := range s.st.Invites {
		if subtle.ConstantTimeCompare(h, []byte(s.st.Invites[i].Hash)) == 1 {
			found = i
		}
	}
	if found >= 0 && !now.Before(s.st.Invites[found].Expires) {
		return -1
	}
	return found
}

// InviteByToken is the usable invite token names.
func (s *FileStore) InviteByToken(token string) (inviteRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := s.inviteIndex(token, s.now())
	if i < 0 {
		return inviteRecord{}, false
	}
	return s.st.Invites[i], true
}

// RedeemInvite uses an invite, once: it makes the account with the password
// hash given, or for a reset sets it and ends the account's sign-ins. The
// account key is returned.
func (s *FileStore) RedeemInvite(token, passwordHash string) (Key, error) {
	now := s.now().UTC()
	var out Key
	err := s.change(func() error {
		i := s.inviteIndex(token, now)
		if i < 0 {
			return ErrNoSuchInvite
		}
		iv := s.st.Invites[i]
		if iv.Key != "" {
			a := s.keyIndex(iv.Key)
			if a < 0 || s.st.Keys[a].Revoked || s.st.Keys[a].Email != iv.Email {
				return ErrNoSuchInvite
			}
			s.st.Keys[a].Password = passwordHash
			s.endSignInsLocked(iv.Key, "")
			out = s.st.Keys[a]
		} else {
			if err := s.accountFreeLocked(iv.Email, iv.User, iv.Tenant); err != nil {
				return err
			}
			k := Key{ID: newID("key_"), User: iv.User, Tenant: iv.Tenant, Scopes: slices.Clone(iv.Scopes),
				// A secret nobody holds: the account signs in by password.
				Hash: hashSecret(newSecret(keySecretPrefix)), Created: now, Email: iv.Email, Password: passwordHash}
			if iv.ExpiresInSecs > 0 {
				t := now.Add(time.Duration(iv.ExpiresInSecs) * time.Second)
				k.Expires = &t
			}
			if iv.Policy != "" {
				if !slices.ContainsFunc(s.st.Policies, func(p api.UserPolicy) bool { return p.Name == iv.Policy }) {
					// Removed since the invite was made: the account would
					// act without the limits it was invited under.
					return ErrNoSuchPolicy
				}
				s.st.UserPolicies = slices.DeleteFunc(s.st.UserPolicies, func(u userPolicyRecord) bool { return u.User == iv.User && u.Tenant == iv.Tenant })
				s.st.UserPolicies = append(s.st.UserPolicies, userPolicyRecord{User: iv.User, Tenant: iv.Tenant, Policy: iv.Policy})
			}
			s.st.Keys = append(s.st.Keys, k)
			out = k
		}
		s.st.Invites = slices.Delete(s.st.Invites, i, i+1)
		return nil
	})
	out.Scopes = slices.Clone(out.Scopes)
	return out, err
}

// endSignInsLocked ends account's sign-ins but keep. Under mu.
func (s *FileStore) endSignInsLocked(account, keep string) {
	s.st.Keys = slices.DeleteFunc(s.st.Keys, func(k Key) bool { return k.Parent == account && k.ID != keep })
}

// Account is the account key for email: one not revoked, possibly expired.
func (s *FileStore) Account(email string) (Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := s.accountIndex(email)
	if i < 0 {
		return Key{}, false
	}
	k := s.st.Keys[i]
	k.Scopes = slices.Clone(k.Scopes)
	return k, true
}

// SignIn mints a key for one sign-in to account, lasting ttl and never past
// the account's own end, and forgets the account's sign-ins that ended.
func (s *FileStore) SignIn(account string, ttl time.Duration) (string, Key, error) {
	now := s.now().UTC()
	secret := newSecret(keySecretPrefix)
	var out Key
	err := s.change(func() error {
		a := s.keyIndex(account)
		if a < 0 || s.st.Keys[a].Email == "" {
			return ErrNotAccount
		}
		acct := &s.st.Keys[a]
		if !acct.Active(now) {
			return ErrKeyRevoked
		}
		end := now.Add(ttl)
		if acct.Expires != nil && acct.Expires.Before(end) {
			end = *acct.Expires
		}
		acct.LastLogin = &now
		out = Key{ID: newID("key_"), User: acct.User, Tenant: acct.Tenant, Scopes: slices.Clone(acct.Scopes),
			Hash: hashSecret(secret), Created: now, Expires: &end, Parent: acct.ID}
		s.st.Keys = slices.DeleteFunc(s.st.Keys, func(k Key) bool { return k.Parent != "" && !k.Active(now) })
		s.st.Keys = append(s.st.Keys, out)
		return nil
	})
	out.Scopes = slices.Clone(out.Scopes)
	return secret, out, err
}

// SignOut forgets a sign-in's key. A key that is not one is refused: an
// account or an issued key is ended by revoking it.
func (s *FileStore) SignOut(id string) error {
	return s.change(func() error {
		i := s.keyIndex(id)
		if i < 0 || s.st.Keys[i].Parent == "" {
			return ErrNotAccount
		}
		s.st.Keys = slices.Delete(s.st.Keys, i, i+1)
		return nil
	})
}

// SetPassword sets account's password hash and ends its sign-ins but keep.
func (s *FileStore) SetPassword(account, passwordHash, keep string) error {
	return s.change(func() error {
		a := s.keyIndex(account)
		if a < 0 || s.st.Keys[a].Email == "" {
			return ErrNotAccount
		}
		s.st.Keys[a].Password = passwordHash
		s.endSignInsLocked(account, keep)
		return nil
	})
}

// accountOf is the account a principal's key belongs to: the key itself
// for an account, its parent for a sign-in's.
func (s *FileStore) accountOf(keyID string) (Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := s.keyIndex(keyID)
	if i < 0 {
		return Key{}, false
	}
	if p := s.st.Keys[i].Parent; p != "" {
		i = s.keyIndex(p)
	}
	if i < 0 || s.st.Keys[i].Email == "" {
		return Key{}, false
	}
	return s.st.Keys[i], true
}

// --- the throttle ---------------------------------------------------------------

// signInThrottle holds back guessing. Failures gateway-wide spend a shared
// bucket; an address with lockAfter failures in a row is locked for
// lockFor, whatever password comes next. One bucket and not one per client
// address: behind hosted Studio every sign-in comes from Studio.
type signInThrottle struct {
	mu      sync.Mutex
	tokens  float64
	last    time.Time
	fails   map[string]*failRecord
	now     func() time.Time
	burst   float64
	per     time.Duration
	lockFor time.Duration
}

type failRecord struct {
	n     int
	until time.Time
	last  time.Time
}

const lockAfter = 5

func newSignInThrottle(now func() time.Time) *signInThrottle {
	return &signInThrottle{tokens: 20, burst: 20, per: 3 * time.Second, lockFor: 15 * time.Minute,
		last: now(), fails: map[string]*failRecord{}, now: now}
}

// allow reports whether a sign-in for email may be tried now.
func (t *signInThrottle) allow(email string) (bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.tokens = min(t.burst, t.tokens+float64(now.Sub(t.last))/float64(t.per))
	t.last = now
	if f := t.fails[email]; f != nil && now.Before(f.until) {
		return false, "too many failed sign-ins for this address; try again in " + f.until.Sub(now).Round(time.Minute).String()
	}
	if t.tokens < 1 {
		return false, "too many failed sign-ins; wait a minute"
	}
	return true, ""
}

func (t *signInThrottle) failed(email string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.tokens = max(0, t.tokens-1)
	f := t.fails[email]
	if f == nil {
		// Bounded: past the cap the oldest failures are forgotten, which
		// can only unlock an address early, never lock one.
		if len(t.fails) >= 10000 {
			for k, o := range t.fails {
				if now.Sub(o.last) > t.lockFor {
					delete(t.fails, k)
				}
			}
		}
		if len(t.fails) >= 10000 {
			return
		}
		f = &failRecord{}
		t.fails[email] = f
	}
	f.n++
	f.last = now
	if f.n >= lockAfter {
		f.until = now.Add(t.lockFor)
		f.n = 0
	}
}

func (t *signInThrottle) succeeded(email string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, email)
}

// --- HTTP -----------------------------------------------------------------------

// openGuard is guard for the routes that take no key: origin, a JSON body,
// capped small.
func (g *Gateway) openGuard(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.originAllowed(r) {
			writeErr(w, http.StatusForbidden, api.CodeForbiddenOrigin, "requests from this origin are not accepted")
			return
		}
		if err := checkContentType(r, false); err != nil {
			writeErr(w, http.StatusUnsupportedMediaType, api.CodeInvalidRequest, err.Error())
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		next(w, r)
	})
}

func signInTTL(secs int) (time.Duration, error) {
	switch d := time.Duration(secs) * time.Second; {
	case secs < 0 || d > loginMaxTTL:
		return 0, fmt.Errorf("ttl_secs: at most %d", int(loginMaxTTL/time.Second))
	case secs == 0:
		return loginTTL, nil
	default:
		return d, nil
	}
}

func loginOf(secret string, k Key, email string) api.Login {
	return api.Login{Secret: secret, User: k.User, Tenant: k.Tenant, Email: email, Scopes: k.Scopes, Expires: *k.Expires}
}

func (g *Gateway) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.LoginRequest
	if !decode(w, r, &req) {
		return
	}
	ttl, err := signInTTL(req.TTLSecs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	email, _ := NormalEmail(req.Email)
	if ok, why := g.signIns.allow(email); !ok {
		writeErr(w, http.StatusTooManyRequests, api.CodeRefused, why)
		return
	}
	acct, found, err := g.store.Account(ctx, email)
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	hash := acct.Password
	if !found || hash == "" {
		hash = dummyHash()
	}
	if !verifyPassword(hash, req.Password) || !found || len(req.Password) > passwordMax {
		g.signIns.failed(email)
		g.logf("sign-in refused for an address (unknown or wrong password)")
		writeErr(w, http.StatusUnauthorized, api.CodeUnauthorized, "that email and password do not match an account")
		return
	}
	g.signIns.succeeded(email)
	// Said only to whoever knows the password.
	if !acct.Active(time.Now()) {
		writeErr(w, http.StatusForbidden, api.CodeRefused, "this account has expired; ask your admin to renew it")
		return
	}
	secret, k, err := g.store.SignIn(ctx, acct.ID, ttl)
	if err != nil {
		writeErr(w, http.StatusForbidden, api.CodeRefused, "this account has expired; ask your admin to renew it")
		return
	}
	g.logf("sign-in to account %s (%s) as key %s", acct.ID, acct.User, k.ID)
	writeJSON(w, http.StatusOK, loginOf(secret, k, acct.Email))
}

func (g *Gateway) inviteFor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.InviteTokenRequest
	if !decode(w, r, &req) {
		return
	}
	iv, ok, err := g.store.InviteByToken(ctx, req.Token)
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, ErrNoSuchInvite.Error())
		return
	}
	writeJSON(w, http.StatusOK, iv.info())
}

func (g *Gateway) register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req api.RegisterRequest
	if !decode(w, r, &req) {
		return
	}
	ttl, err := signInTTL(req.TTLSecs)
	if err == nil {
		err = checkPassword(req.Password)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	// Checked before the hash is paid for, and again as it is used.
	if _, ok, err := g.store.InviteByToken(ctx, req.Token); err != nil {
		g.writeStoreErr(w, err)
		return
	} else if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, ErrNoSuchInvite.Error())
		return
	}
	acct, err := g.store.RedeemInvite(ctx, req.Token, hashPassword(req.Password))
	switch {
	case errors.Is(err, ErrNoSuchInvite):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, err.Error())
		return
	case errors.Is(err, ErrEmailTaken), errors.Is(err, ErrUserHasAccount), errors.Is(err, ErrMemberName), errors.Is(err, ErrNoSuchPolicy):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error()+"; ask your admin for a new invite")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "making the account failed")
		return
	}
	g.logf("invite used: account %s for %s", acct.ID, acct.User)
	// A reset ended every sign-in; the accessChanged after it ends what
	// they had open.
	g.accessChanged()
	secret, k, err := g.store.SignIn(ctx, acct.ID, ttl)
	if err != nil {
		writeErr(w, http.StatusForbidden, api.CodeRefused, "this account has expired; ask your admin to renew it")
		return
	}
	writeJSON(w, http.StatusOK, loginOf(secret, k, acct.Email))
}

func (g *Gateway) logout(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if err := g.store.SignOut(ctx, p.KeyID); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "this key is not a sign-in's; an admin revokes it")
		return
	}
	g.accessChanged()
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) changePassword(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	var req api.PasswordRequest
	if !decode(w, r, &req) {
		return
	}
	acct, ok, err := g.store.AccountOf(ctx, p.KeyID)
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "this key is not an account's; it has no password")
		return
	}
	if err := checkPassword(req.New); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	if ok, why := g.signIns.allow(acct.Email); !ok {
		writeErr(w, http.StatusTooManyRequests, api.CodeRefused, why)
		return
	}
	if len(req.Current) > passwordMax || !verifyPassword(acct.Password, req.Current) {
		g.signIns.failed(acct.Email)
		writeErr(w, http.StatusForbidden, api.CodeRefused, "the current password is not right")
		return
	}
	if err := g.store.SetPassword(ctx, acct.ID, hashPassword(req.New), p.KeyID); err != nil {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "changing the password failed")
		return
	}
	g.logf("password changed for account %s", acct.ID)
	g.accessChanged()
	w.WriteHeader(http.StatusNoContent)
}

// --- admin --------------------------------------------------------------------

func (g *Gateway) adminCreateInvite(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.CreateInviteRequest
	if !decode(w, r, &req) {
		return
	}
	if req.SendEmail && g.cfg.Mail == nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "this gateway sends no email: it was started without --smtp-host")
		return
	}
	token, iv, err := g.store.CreateInvite(ctx, req)
	switch {
	case errors.Is(err, ErrEmailTaken), errors.Is(err, ErrUserHasAccount), errors.Is(err, ErrMemberName):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	case errors.Is(err, ErrNoSuchPolicy):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "policy "+req.Policy+": no such policy")
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	g.logf("invite %s for %s made by %s", iv.ID, iv.User, p.KeyID)
	out := api.CreatedInvite{InviteInfo: iv.info(), Token: token}
	if req.SendEmail {
		g.emailInvite(r.Context(), &out, iv, token)
	}
	writeJSON(w, http.StatusCreated, out)
}

func (g *Gateway) adminListInvites(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeAdmin) {
		return
	}
	out := api.InviteList{Invites: []api.InviteInfo{}, CanEmail: g.cfg.Mail != nil}
	ivs, err := g.store.Invites(ctx)
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	for _, iv := range ivs {
		out.Invites = append(out.Invites, iv.info())
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) adminRevokeInvite(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeAdmin) {
		return
	}
	id := r.PathValue("id")
	if err := g.store.RevokeInvite(ctx, id); err != nil {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such invite")
		return
	}
	g.logf("invite %s withdrawn by %s", id, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}

// adminResendInvite makes a pending invite's link again, and emails it
// when asked: the token is not kept, so the old link cannot be sent twice.
func (g *Gateway) adminResendInvite(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.ResetPasswordRequest
	if !decode(w, r, &req) {
		return
	}
	if req.SendEmail && g.cfg.Mail == nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "this gateway sends no email: it was started without --smtp-host")
		return
	}
	id := r.PathValue("id")
	token, iv, err := g.store.RenewInvite(ctx, id, inviteValid)
	if err != nil {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such invite: used, withdrawn or expired")
		return
	}
	g.logf("invite %s made again by %s", iv.ID, p.KeyID)
	out := api.CreatedInvite{InviteInfo: iv.info(), Token: token}
	if req.SendEmail {
		g.emailInvite(r.Context(), &out, iv, token)
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) adminResetPassword(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.ResetPasswordRequest
	if !decode(w, r, &req) {
		return
	}
	if req.SendEmail && g.cfg.Mail == nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "this gateway sends no email: it was started without --smtp-host")
		return
	}
	id := r.PathValue("id")
	token, iv, err := g.store.CreateResetInvite(ctx, id, inviteValid)
	switch {
	case errors.Is(err, ErrNoSuchKey):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such key")
		return
	case errors.Is(err, ErrNotAccount):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "this key is not an account; it has no password")
		return
	case errors.Is(err, ErrKeyRevoked):
		writeErr(w, http.StatusConflict, api.CodeConflict, "the account is revoked; invite the address again")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "making the reset failed")
		return
	}
	g.logf("password reset %s for key %s made by %s", iv.ID, id, p.KeyID)
	out := api.CreatedInvite{InviteInfo: iv.info(), Token: token}
	if req.SendEmail {
		g.emailInvite(r.Context(), &out, iv, token)
	}
	writeJSON(w, http.StatusCreated, out)
}
