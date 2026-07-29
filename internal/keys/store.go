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

	replaced := false
	for i, existing := range records {
		if existing.Name == stored.Name {
			// Keep the original creation time. Rotating a credential is not creating a new one,
			// and losing the date would hide how long a key has been in use.
			stored.CreatedAt = existing.CreatedAt
			stored.LastUsedAt = existing.LastUsedAt

			// The models its owner added survive too, for the same reason the creation date does.
			// Rotating a credential is not setting up a new one, and a list somebody built by hand
			// is not something to make them build again because their key expired.
			stored.Models = existing.Models

			// And keep the rate, unless the caller supplied one. Rotating a key does not change
			// what the endpoint charges, so silently dropping the price would turn a working cost
			// figure into "unknown" for no reason the user could see.
			if meta.Rate.IsZero() {
				stored.InputPerMTok = existing.InputPerMTok
				stored.OutputPerMTok = existing.OutputPerMTok
				stored.CacheReadPerMTok = existing.CacheReadPerMTok
			}
			records[i] = stored
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, stored)
	}

	if err := s.save(records); err != nil {
		return core.KeyMetadata{}, err
	}
	return stored.toMetadata(), nil
}

// Get returns a credential's secret value.
func (s *Store) Get(ref core.KeyRef) (core.Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return core.Secret{}, err
	}
	if _, ok := findRecord(records, ref.Name); !ok {
		return core.Secret{}, fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
	}

	value, err := s.backend.Get(ref.Name)
	if errors.Is(err, ErrNotFound) {
		// The two halves disagree. Say so precisely, because "not found" would send the user
		// looking for a key they can see in `canopy keys list`.
		return core.Secret{}, fmt.Errorf(
			"key %q is recorded but its secret is missing from the %s backend, "+
				"so it was removed outside Canopy. Add it again with `canopy keys add %s`",
			ref.Name, s.backend.Name(), ref.Name)
	}
	if err != nil {
		return core.Secret{}, err
	}
	return core.NewSecret(value), nil
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

// SetModel records which model this credential talks to.
//
// Separate from Put because changing the model must not require re-entering the secret. Somebody
// correcting a typo in a model id should not have to go and find their API key again, and a flow
// that asked them to would get the key pasted from somewhere less careful than where it lives now.
func (s *Store) SetModel(ref core.KeyRef, model string) error {
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
