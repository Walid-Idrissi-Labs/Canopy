package keys

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// BackendEnvVar selects the secret backend. The only value that changes anything is "file".
const BackendEnvVar = "CANOPY_KEY_BACKEND"

// Store holds provider credentials and the non secret facts about them.
//
// Secrets and metadata are kept in different places on purpose. Secrets go to the OS credential
// store, which is good at protecting a value and useless at holding structure. Metadata, meaning
// provider, endpoint, fingerprint and timestamps, goes to a JSON file, which is the opposite.
//
// The cost of splitting them is that the two can disagree, so that case is handled explicitly
// rather than hoped away: a credential whose metadata exists but whose secret has vanished is
// reported as exactly that, not as missing and not as present.
type Store struct {
	mu      sync.Mutex
	backend Backend
	path    string
	clock   func() time.Time
}

// record is the on disk shape of one credential's metadata. No secret appears here, by design.
type record struct {
	Name        string     `json:"name"`
	Provider    string     `json:"provider"`
	BaseURL     string     `json:"baseUrl,omitempty"`
	Model       string     `json:"model,omitempty"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`

	// Kind, Account and ExpiresAt are what a credential somebody signed in to carries in place of a
	// value they pasted. None of the three is a secret, and that is the point of them being here
	// rather than in the backend: a list can say who a credential is signed in as and when it stops
	// working without unlocking a keychain to find out. The tokens themselves are in the backend.
	// See Kind in signin.go for why the kind sits beside the provider instead of becoming one.
	//
	// Absent on a pasted credential rather than spelled out, the same way Models is absent when it
	// is empty, so a keys.json written before any of this existed reads back as the document it
	// already was and no record starts making a claim about itself that nobody made.
	Kind      string     `json:"kind,omitempty"`
	Account   string     `json:"account,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	// Route is which way in produced this grant: "copilot", and whatever S-04 and S-05 call theirs.
	//
	// Here rather than derived from Provider because Provider cannot answer it. Copilot and Codex are
	// both openai-compatible, so the moment the second of them lands a switch on Provider sends one
	// of them to the other's client and to the other's token endpoint. SourceFor at refresh.go:93 was
	// written as a function rather than a map for exactly this, and this is the field it keys on.
	//
	// Optional, and an absent route is not damage. A credential signed in on a build that predates
	// this field is still a credential, and the place that needs the route says so plainly rather
	// than guessing. Requiring it would also mean rewriting tests belonging to two tasks currently
	// in review, which is a worse trade than one legible error.
	Route string `json:"route,omitempty"`

	// Rate is the user's own price for this credential, per million tokens. Absent until they set
	// one, which is why every field is omitempty: a rate of zero written to disk and a rate never
	// set would otherwise be the same document.
	InputPerMTok     float64 `json:"inputPerMTok,omitempty"`
	OutputPerMTok    float64 `json:"outputPerMTok,omitempty"`
	CacheReadPerMTok float64 `json:"cacheReadPerMTok,omitempty"`

	// Models are the models this credential's owner added by hand, beside whatever the catalog
	// already knows about the provider. Plural here rather than in core.KeyMetadata, which keeps its
	// single Model field as the selected default: a frozen contract does not grow a field for
	// something the layer above it can carry. See D-46.
	//
	// Omitted when empty, so a file written before this existed reads back identical to one written
	// by a build that has it and never added anything.
	Models []storedModel `json:"models,omitempty"`
}

// storedModel is one user-added model on disk.
//
// Its own type rather than the catalog's, because these two field names end up in a file people
// edit and read, and a struct meant for a Go API is not a document format.
type storedModel struct {
	ID string `json:"id"`

	// Name is what a person reads. Optional, because "gpt-5.2" needs no help and
	// "minimaxai/minimax-m2.7" does.
	Name string `json:"name,omitempty"`
}

// Open returns the key store, choosing a backend from the environment.
//
// The keychain is the default. The file backend has to be asked for by name, and Open reports
// which one is in use so a caller can say so out loud.
func Open() (*Store, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}

	var backend Backend
	if os.Getenv(BackendEnvVar) == "file" {
		backend = &fileBackend{path: filepath.Join(dir, "credentials.json")}
	} else {
		backend = keychainBackend{}
	}

	return NewStore(backend, filepath.Join(dir, "keys.json")), nil
}

// NewStore builds a store over a given backend and metadata path. For tests and for callers that
// want to choose explicitly.
func NewStore(backend Backend, metadataPath string) *Store {
	return &Store{
		backend: backend,
		path:    metadataPath,
		clock:   time.Now,
	}
}

// SetClock replaces the clock. For tests.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = now
}

// BackendName says where secrets are being kept.
func (s *Store) BackendName() string { return s.backend.Name() }

// UsingInsecureBackend reports whether secrets are being written to a plain file.
//
// Exposed so the interface can keep saying so. A one time warning when the backend is chosen is
// not enough, because the person who sees it is often not the person who later assumes their keys
// are in the keychain.
func (s *Store) UsingInsecureBackend() bool {
	_, isFile := s.backend.(*fileBackend)
	return isFile
}

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the configuration directory: %w", err)
	}
	return filepath.Join(base, "canopy"), nil
}

func (s *Store) load() ([]record, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading key metadata: %w", err)
	}
	var records []record
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("the key metadata at %s is corrupt: %w", s.path, err)
	}
	return records, nil
}

func (s *Store) save(records []record) error {
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding key metadata: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating the configuration directory: %w", err)
	}
	return writeFileAtomic(s.path, data, 0o600)
}

func (r record) toMetadata() core.KeyMetadata {
	return core.KeyMetadata{
		Ref:         core.KeyRef{Name: r.Name, Provider: core.Provider(r.Provider)},
		BaseURL:     r.BaseURL,
		Model:       r.Model,
		Fingerprint: r.Fingerprint,
		CreatedAt:   r.CreatedAt,
		LastUsedAt:  r.LastUsedAt,
		Rate: core.KeyRate{
			InputPerMTok:     r.InputPerMTok,
			OutputPerMTok:    r.OutputPerMTok,
			CacheReadPerMTok: r.CacheReadPerMTok,
		},
	}
}

// Put stores a credential, replacing any existing one of the same name.
//
// The secret is written to the backend before the metadata is recorded. That order matters: if the
// second step fails, there is an orphaned secret nobody can reach, which is harmless. The other
// order would leave metadata claiming a credential exists when it does not, which is a lie the
// rest of the system would act on.
func (s *Store) Put(meta core.KeyMetadata, secret core.Secret) (core.KeyMetadata, error) {
	if err := meta.Ref.Validate(); err != nil {
		return core.KeyMetadata{}, err
	}
	if secret.IsZero() {
		return core.KeyMetadata{}, fmt.Errorf("key %q has no value", meta.Ref.Name)
	}
	if meta.Ref.Provider.RequiresBaseURL() && meta.BaseURL == "" {
		return core.KeyMetadata{}, fmt.Errorf(
			"key %q uses provider %q, which needs a base URL", meta.Ref.Name, meta.Ref.Provider)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return core.KeyMetadata{}, err
	}

	if err := s.backend.Set(meta.Ref.Name, secret.Reveal()); err != nil {
		return core.KeyMetadata{}, err
	}

	stored := record{
		Name:             meta.Ref.Name,
		Provider:         string(meta.Ref.Provider),
		BaseURL:          meta.BaseURL,
		Model:            meta.Model,
		Fingerprint:      secret.Fingerprint(),
		CreatedAt:        s.clock(),
		InputPerMTok:     meta.Rate.InputPerMTok,
		OutputPerMTok:    meta.Rate.OutputPerMTok,
		CacheReadPerMTok: meta.Rate.CacheReadPerMTok,
	}

	records, stored = upsert(records, stored, meta.Rate)

	if err := s.save(records); err != nil {
		return core.KeyMetadata{}, err
	}
	return stored.toMetadata(), nil
}

// upsert files a record under its name, carrying over what an existing one of that name owns.
//
// One function rather than a copy in each of Put and PutSignIn, because the rule is one rule and
// the two must not be able to drift apart: replacing what a credential authenticates with is not
// setting up a new credential, whether the replacement is a pasted value or a fresh sign-in. What
// survives is what belongs to the credential rather than to the value behind it.
func upsert(records []record, stored record, rate core.KeyRate) ([]record, record) {
	for i, existing := range records {
		if existing.Name != stored.Name {
			continue
		}

		// Keep the original creation time. Rotating a credential is not creating a new one, and
		// losing the date would hide how long a key has been in use.
		stored.CreatedAt = existing.CreatedAt
		stored.LastUsedAt = existing.LastUsedAt

		// The models its owner added survive too, for the same reason the creation date does.
		// Rotating a credential is not setting up a new one, and a list somebody built by hand is
		// not something to make them build again because their key expired.
		stored.Models = existing.Models

		// And keep the rate, unless the caller supplied one. Rotating a key does not change what
		// the endpoint charges, so silently dropping the price would turn a working cost figure
		// into "unknown" for no reason the user could see.
		if rate.IsZero() {
			stored.InputPerMTok = existing.InputPerMTok
			stored.OutputPerMTok = existing.OutputPerMTok
			stored.CacheReadPerMTok = existing.CacheReadPerMTok
		}

		records[i] = stored
		return records, stored
	}
	return append(records, stored), stored
}

// Get returns a credential's secret value.
//
// Only a pasted one. A credential somebody signed in to is refused here rather than served, because
// what sits behind it is a pair of tokens, and a caller reaching for Get is reaching for something
// to put in a header. Tokens is the way to those, and it says so.
func (s *Store) Get(ref core.KeyRef) (core.Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return core.Secret{}, err
	}
	found, ok := findRecord(records, ref.Name)
	if !ok {
		return core.Secret{}, fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
	}
	if in := found.signIn(); in.Kind.IsSignIn() {
		return core.Secret{}, fmt.Errorf(
			"key %q is signed in as %s, so there is no pasted value to read", ref.Name, in.Account)
	}

	value, err := s.backend.Get(ref.Name)
	if errors.Is(err, ErrNotFound) {
		// The two halves disagree. Say so precisely, because "not found" would send the user
		// looking for a key they can see in `canopy keys list`.
		return core.Secret{}, s.halvesDisagree(ref.Name,
			fmt.Sprintf("Add it again with `canopy keys add %s`", ref.Name))
	}
	if err != nil {
		return core.Secret{}, err
	}

	if _, isGrant := parseGrant(value); isGrant {
		// The record says pasted and the backend holds a sign-in's tokens, which is what a change
		// that stopped between its two steps leaves behind. Refused rather than returned, because
		// returning it means a request built with a JSON document where the credential belongs,
		// answered with a 401, and reported as a wrong key: ErrAuthentication is documented as
		// never retry and never fall back, so the user would be sent to replace a key that is fine.
		return core.Secret{}, fmt.Errorf(
			"key %q is recorded as a pasted secret but the %s backend holds a sign-in's tokens "+
				"under its name, which is what a change that stopped halfway leaves behind. "+
				"Add it again with `canopy keys add %s`", ref.Name, s.backend.Name(), ref.Name)
	}
	return core.NewSecret(value), nil
}

// halvesDisagree describes metadata the user can see with nothing behind it.
//
// One sentence shared by pasted credentials and sign-ins, because it is one situation and the
// person reading it has one problem. Only the remedy is passed in, and only because it genuinely
// differs: telling somebody to paste a key they never had would send them looking for a thing that
// does not exist.
func (s *Store) halvesDisagree(name, remedy string) error {
	return fmt.Errorf(
		"key %q is recorded but its secret is missing from the %s backend, so it was removed "+
			"outside Canopy. %s", name, s.backend.Name(), remedy)
}

// Metadata returns the non secret facts about a credential.
func (s *Store) Metadata(ref core.KeyRef) (core.KeyMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return core.KeyMetadata{}, err
	}
	found, ok := findRecord(records, ref.Name)
	if !ok {
		return core.KeyMetadata{}, fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
	}
	return found.toMetadata(), nil
}

// List returns every stored credential's metadata, ordered by name.
func (s *Store) List() ([]core.KeyMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]core.KeyMetadata, 0, len(records))
	for _, r := range records {
		out = append(out, r.toMetadata())
	}
	return out, nil
}

// Remove deletes a credential and its metadata.
//
// The secret goes first here too. If metadata removal then fails, the leftover entry is visible
// and fixable. The reverse would leave a secret in the keychain that nothing knows about, which is
// a credential nobody will ever think to revoke.
//
// A sign-in's tokens go with it and need no line of their own, because both of them live in the one
// backend entry under the credential's name. That is the main thing bought by keeping them there
// rather than under names derived from it, and the argument is in signin.go.
func (s *Store) Remove(ref core.KeyRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := findRecord(records, ref.Name); !ok {
		return fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
	}

	if err := s.backend.Delete(ref.Name); err != nil {
		return err
	}

	remaining := make([]record, 0, len(records))
	for _, r := range records {
		if r.Name != ref.Name {
			remaining = append(remaining, r)
		}
	}
	return s.save(remaining)
}

// Rename changes what a credential is called, keeping the credential.
//
// A name here is not a label, it is the identifier the secret is filed under in the backend and the
// one every conversation writes down, so this is a move rather than an edit of a field. The old name
// stops resolving the moment it returns, which is why the callers that hold conversations are
// expected to follow it: see Engine.RenameCredential.
//
// The order is the one Put and Remove already argue for, in both directions. When Canopy holds a
// backend value, it is written under the new name before anything else changes, so a failure there
// leaves the credential exactly as it was. The metadata moves next, because that is the step that
// makes the new name real. Only then is the old value deleted, because a delete that ran first and
// was followed by a failure would have thrown away a credential to rename it. A delegated sign-in
// deliberately has no backend value, so its move is the metadata step alone.
//
// Two failures leave something behind and both say so rather than being swallowed. A metadata write
// that fails takes the freshly written secret with it, so nothing is left under a name the records
// have never heard of. A delete that fails afterwards leaves the same value readable under the old
// name, which is a credential nobody would think to revoke, so it is reported as exactly that.
func (s *Store) Rename(ref core.KeyRef, to string) (core.KeyMetadata, error) {
	to = strings.TrimSpace(to)
	if err := core.ValidateKeyName(to); err != nil {
		return core.KeyMetadata{}, err
	}
	if to == ref.Name {
		// Not an error. Somebody who opened the field, changed their mind and pressed enter has asked
		// for the state the store is already in, and failing them would be inventing a problem.
		return s.Metadata(ref)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return core.KeyMetadata{}, err
	}
	found, ok := findRecord(records, ref.Name)
	if !ok {
		return core.KeyMetadata{}, fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
	}
	if _, taken := findRecord(records, to); taken {
		// Refused rather than merged. Two credentials with one name is one credential, and which of
		// the two secrets survived would be decided by the order of a loop.
		return core.KeyMetadata{}, fmt.Errorf(
			"there is already a credential called %q, so pick another name or remove that one first", to)
	}

	delegated := found.signIn().Kind == KindDelegated
	var backendValue string
	if !delegated {
		backendValue, err = s.backend.Get(ref.Name)
		if errors.Is(err, ErrNotFound) {
			// The same disagreement Get explains, said here rather than reported as a rename failure:
			// a record whose secret has gone is not a record that can be moved anywhere useful.
			return core.KeyMetadata{}, fmt.Errorf(
				"key %q is recorded but its secret is missing from the %s backend, so there is nothing to "+
					"rename. Add it again with `canopy keys add %s`", ref.Name, s.backend.Name(), ref.Name)
		}
		if err != nil {
			return core.KeyMetadata{}, err
		}

		if err := s.backend.Set(to, backendValue); err != nil {
			return core.KeyMetadata{}, err
		}
	}

	for i := range records {
		if records[i].Name == ref.Name {
			records[i].Name = to
			break
		}
	}
	if err := s.save(records); err != nil {
		if delegated {
			return core.KeyMetadata{}, err
		}
		// Nothing claims the new name, so the secret written under it is unreachable and is taken
		// back. If that cleanup fails too, both facts matter: the metadata and old secret still name
		// the credential correctly, but an untracked copy is now live under the proposed name.
		if cleanupErr := s.backend.Delete(to); cleanupErr != nil {
			return core.KeyMetadata{}, fmt.Errorf(
				"saving the rename from %q to %q failed: %v; cleanup also failed, so an "+
					"untracked copy of the secret remains under %q in the %s backend: %w. "+
					"Delete or revoke that copy there",
				ref.Name, to, err, to, s.backend.Name(), cleanupErr)
		}
		return core.KeyMetadata{}, err
	}

	if !delegated {
		if err := s.backend.Delete(ref.Name); err != nil {
			found.Name = to
			return found.toMetadata(), fmt.Errorf(
				"%q is now called %q, but its old secret could not be removed from the %s backend: %w. "+
					"The same value is still readable under %q, so revoke or delete it there",
				ref.Name, to, s.backend.Name(), err, ref.Name)
		}
	}

	found.Name = to
	return found.toMetadata(), nil
}

// SetModel records which model this credential talks to.
//
// Separate from Put because changing the model must not require re-entering the secret. Somebody
// correcting a typo in a model id should not have to go and find their API key again, and a flow
// that asked them to would get the key pasted from somewhere less careful than where it lives now.
func (s *Store) SetModel(ref core.KeyRef, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}

	for i, existing := range records {
		if existing.Name != ref.Name {
			continue
		}
		records[i].Model = strings.TrimSpace(model)
		return s.save(records)
	}
	return fmt.Errorf("no credential called %q", ref.Name)
}

// Models returns the models this credential's owner added by hand.
//
// Only theirs. What the provider is known to run is internal/catalog's answer and is the same for
// every key on that provider, so holding a copy per credential would be a copy that goes stale. The
// callers that want both put the two together, in that order, which is also the order they are
// offered in.
func (s *Store) Models(ref core.KeyRef) ([]catalog.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return nil, err
	}
	found, ok := findRecord(records, ref.Name)
	if !ok {
		return nil, fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
	}

	out := make([]catalog.Model, 0, len(found.Models))
	for _, model := range found.Models {
		out = append(out, catalog.Model{ID: model.ID, Name: model.Name})
	}
	return out, nil
}

// AddModel records a model this credential can be pointed at.
//
// Adding one does not select it. The two are separate on purpose: somebody teaching Canopy about
// three models their gateway serves is not thereby saying which one their next conversation should
// run on, and a call that did both would make the list impossible to build without changing what is
// running each time.
//
// An id that is already there has its name updated rather than being added twice, which is what
// makes this the way to correct a name as well as the way to add one.
func (s *Store) AddModel(ref core.KeyRef, id, name string) error {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" {
		return errors.New("a model id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}

	for i, r := range records {
		if r.Name != ref.Name {
			continue
		}
		for j, existing := range r.Models {
			if existing.ID == id {
				records[i].Models[j].Name = name
				return s.save(records)
			}
		}
		// An id that is not this one byte for byte but is the same model once spelling is forgiven.
		// Refused rather than stored, because both would then be offered and the resolver would have
		// to choose between two spellings of one thing every time somebody named it.
		//
		// Refused rather than quietly folded into the existing entry, too. What is stored goes on
		// the wire exactly as it was typed: an unknown provider's ids may well be case sensitive,
		// and correcting somebody's capitalisation for them is how a request starts failing at the
		// far end for a reason nothing on this side explains.
		for _, existing := range r.Models {
			if catalog.SameModel(existing.ID, id) {
				return fmt.Errorf(
					"key %q already offers %q, which is the same model as %q once case and "+
						"punctuation are forgiven. Remove that one first if this spelling is the "+
						"one your endpoint wants", ref.Name, existing.ID, id)
			}
		}
		records[i].Models = append(records[i].Models, storedModel{ID: id, Name: name})
		return s.save(records)
	}
	return fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
}

// RemoveModel forgets a model its owner added.
//
// Refused rather than ignored when the id is not there, because the usual reason for a miss is a
// typo, and a silent success would leave somebody believing they had removed something they had not.
func (s *Store) RemoveModel(ref core.KeyRef, id string) error {
	id = strings.TrimSpace(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}

	for i, r := range records {
		if r.Name != ref.Name {
			continue
		}
		for j, existing := range r.Models {
			if existing.ID == id {
				records[i].Models = append(r.Models[:j:j], r.Models[j+1:]...)
				return s.save(records)
			}
		}
		return fmt.Errorf("key %q has no model called %q that was added by hand", ref.Name, id)
	}
	return fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
}

// SetRate records what the user says this credential charges.
//
// A separate call rather than a field on Put, because setting a price must not require re typing a
// secret. Somebody correcting a rate they got wrong should not have to go and find their API key
// again, and a flow that asks for one is a flow where people paste keys into shell history.
func (s *Store) SetRate(ref core.KeyRef, rate core.KeyRate) error {
	if !rate.IsZero() {
		if err := rate.Validate(); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}

	for i, r := range records {
		if r.Name != ref.Name {
			continue
		}
		records[i].InputPerMTok = rate.InputPerMTok
		records[i].OutputPerMTok = rate.OutputPerMTok
		records[i].CacheReadPerMTok = rate.CacheReadPerMTok
		return s.save(records)
	}
	return fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
}

// MarkUsed records that a credential was used just now.
func (s *Store) MarkUsed(ref core.KeyRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}
	now := s.clock()
	for i, r := range records {
		if r.Name == ref.Name {
			records[i].LastUsedAt = &now
			return s.save(records)
		}
	}
	return fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
}

func findRecord(records []record, name string) (record, bool) {
	for _, r := range records {
		if r.Name == name {
			return r, true
		}
	}
	return record{}, false
}
