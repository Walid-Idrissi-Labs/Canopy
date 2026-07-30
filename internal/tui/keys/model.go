// Package keys is the credential management screen.
//
// It talks to a Store interface rather than the concrete key store, so it can be driven in tests
// without touching a keychain, and so this package never has to know where credentials actually
// live.
package keys

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// Store is the part of the credential store this screen needs.
//
// Narrow on purpose. The screen can list, add and remove, and there is deliberately no method here
// that returns a secret value, so no amount of mistakes in this package can render one.
type Store interface {
	List() ([]core.KeyMetadata, error)
	Put(meta core.KeyMetadata, secret core.Secret) (core.KeyMetadata, error)
	Remove(ref core.KeyRef) error
	SetModel(ref core.KeyRef, model string) error

	// Models is what this key's owner added by hand, which the screen offers after the catalog.
	Models(ref core.KeyRef) ([]catalog.Model, error)

	// AddModel is how a model the catalog has never heard of joins the list. Removing one stays with
	// the CLI: this interface is narrow on purpose, and a screen that can delete something a person
	// spent effort recording wants a confirmation of its own before it is worth having.
	AddModel(ref core.KeyRef, id, name string) error

	// Identity is who a credential is signed in as, and the zero value for one somebody pasted.
	//
	// The narrowness survives this: an account and an expiry are facts the vendor publishes about a
	// person, not secrets, and the point of S-01 keeping them out of the keychain half is that a
	// list can draw them without unlocking anything. There is still no method here that returns a
	// secret and there is still nowhere for one to appear.
	Identity(ref core.KeyRef) (Identity, error)

	BackendName() string
	UsingInsecureBackend() bool
}

type mode int

const (
	modeList mode = iota
	modeName
	modeProvider
	modeBaseURL
	modeModelPick
	modeModel
	modeSecret

	// modeSignIn is the branch that reaches a stored credential without a secret prompt. Every other
	// path through this machine ends at modeSecret, because before phase S every credential was a
	// string somebody held. There is nothing to hold on a subscription, so this step shows what the
	// vendor needs shown and waits.
	modeSignIn

	modeConfirmRemove
)

// Model is the credential screen.
type Model struct {
	store Store

	// signIn is how a subscription is signed in to, and nil in a build with no routes. Nil is a
	// legitimate state rather than a missing dependency: the provider list is then exactly what it
	// was before phase S, which is the honest thing to show when there is no way in to offer.
	signIn SignIn

	mode   mode
	keys   []core.KeyMetadata
	cursor int

	// identities is who each credential is signed in as, read once per reload rather than per frame.
	// Keyed by credential name, which is what the list has in hand while it draws.
	identities map[string]Identity

	// draft holds the credential being added.
	draftName     string
	draftProvider core.Provider
	draftBaseURL  string
	draftModel    string

	// draftRoute is the way in being signed in through, held so the step can say which vendor it is
	// waiting on and so a caveat is on screen before anything is stored.
	draftRoute Route

	// attempt is the sign-in in flight, and attemptID is which one it is. The number is what makes a
	// cancelled sign-in stay cancelled: an answer that arrives after somebody pressed escape carries
	// the old number and is dropped rather than being taken for the next attempt's.
	attempt   Attempt
	attemptID int

	// signingIn is whether the vendor has been asked and has not answered, which is the difference
	// between "nothing to show yet" and "nothing to show at all".
	signingIn bool

	// prompt is what the vendor needs the person to do: a code, a page, or an explanation of what is
	// being waited on.
	prompt Prompt

	// draftSecret is the one place in this design where a credential exists as a plain string, and
	// it is unavoidable: the user is typing it. It is cleared the moment it reaches the store, and
	// it is never rendered, only its length is.
	draftSecret string

	providerCursor int

	// modelChoices is what the model picker is offering: what the catalog knows about the key's
	// provider and endpoint, then whatever its owner added that the catalog does not have.
	modelChoices []catalog.Model
	modelCursor  int

	// editing is set when the model prompt was reached from the list rather than from the add flow,
	// so committing it changes one field instead of trying to store a credential with no secret.
	editing bool

	// chosen is the credential the user picked for this conversation. The application reads it and
	// clears nothing: the screen states a preference, and switching sessions is the parent's job.
	chosen string

	// storedChoice distinguishes a selection produced by the add wizard from one made on the list.
	// Both require the parent conversation to acknowledge the switch, but a refusal after the wizard
	// must still say that the credential was safely stored.
	storedChoice bool

	status string
	err    error

	width int
}

// New builds the credential screen with no way to sign in, which is every build before phase S and
// every test that is about something else.
func New(store Store) Model { return NewWithSignIn(store, nil) }

// NewWithSignIn builds the credential screen with the routes a subscription can be signed in
// through.
//
// A second constructor rather than a field set afterwards, because a screen whose sign-in arrives
// later has a window in which its provider list is wrong, and the list is drawn from the first
// frame.
func NewWithSignIn(store Store, signIn SignIn) Model {
	m := Model{store: store, signIn: signIn, draftProvider: core.ProviderAnthropic, width: 80}
	m.reload()
	return m
}

func (m *Model) reload() {
	all, err := m.store.List()
	if err != nil {
		m.err = err
		return
	}
	m.keys = all
	m.err = nil
	if m.cursor >= len(m.keys) {
		m.cursor = max(0, len(m.keys)-1)
	}

	// Read here rather than while drawing. A row is drawn on every keystroke and a lookup that goes
	// to disk once per row per frame is a screen that stutters for a reason nobody can see.
	m.identities = make(map[string]Identity, len(m.keys))
	for _, key := range m.keys {
		identity, err := m.store.Identity(key.Ref)
		if err != nil {
			// Not fatal to the list. A credential whose sign-in could not be read is still a
			// credential, and hiding the whole list over one row is worse than a row with less on it.
			continue
		}
		m.identities[key.Ref.Name] = identity
	}
}

// identityOf is what the list knows about one credential's sign-in.
func (m Model) identityOf(name string) Identity { return m.identities[name] }

// IsEmpty reports whether no credentials are stored, which is what makes this the first thing a
// new user sees.
func (m Model) IsEmpty() bool { return len(m.keys) == 0 }

// Adding reports whether the screen is mid way through adding a credential, so a parent model
// knows not to steal keystrokes.
func (m Model) Adding() bool { return m.mode != modeList && m.mode != modeConfirmRemove }

// Chosen returns the credential the user picked, and whether they picked one.
//
// Read by the application rather than acted on here, because which credential a conversation runs
// on is a fact about the conversation, and this screen does not own one.
func (m Model) Chosen() (string, bool) { return m.chosen, m.chosen != "" }

// SelectionApplied acknowledges that the parent conversation accepted Chosen.
//
// The credential screen cannot make this claim itself: the engine may refuse while an answer is in
// flight. Keeping the acknowledgement explicit prevents "is now the credential" from appearing
// before the component that owns the session has actually changed it.
func (m *Model) SelectionApplied(name string) {
	if name == "" || m.chosen != name {
		return
	}
	if m.storedChoice {
		m.status += " It is now the credential for this conversation."
	} else {
		m.status = fmt.Sprintf("%s is now the credential for this conversation.", name)
	}
	m.chosen = ""
	m.storedChoice = false
	m.err = nil
}

// SelectionRefused records that the parent conversation did not accept Chosen.
//
// The preference is cleared so an unrelated later keystroke cannot retry it after a running turn
// ends and silently apply a choice the user already saw refused.
func (m *Model) SelectionRefused(name, reason string) {
	if name == "" || m.chosen != name {
		return
	}
	if reason == "" {
		reason = "the conversation did not accept the switch"
	}
	if m.storedChoice {
		m.err = fmt.Errorf("stored %q, but it is not selected for this conversation: %s", name, reason)
	} else {
		m.err = fmt.Errorf("%q was not selected for this conversation: %s", name, reason)
	}
	m.chosen = ""
	m.storedChoice = false
	m.status = ""
}

// Model returns the model the chosen credential talks to, for the caller that has to set both.
func (m Model) ModelFor(name string) string {
	for _, key := range m.keys {
		if key.Ref.Name == name {
			return key.Model
		}
	}
	return ""
}

func (m Model) Init() tea.Cmd { return nil }

// Update handles a message.
//
// The handlers below take pointer receivers deliberately. An earlier version used value receivers
// and passed &m.draftName into a shared text handler, which silently discarded every keystroke:
// the pointer referred to the caller's copy while the method returned its own, taken before the
// mutation. Pointer receivers on a local, addressable value remove that entire class of bug rather
// than requiring everyone to remember it.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		cmd := m.handleKey(msg)
		return m, cmd

	// A sign-in waits on a browser, a device code or another process, which is minutes rather than
	// milliseconds. Bubble Tea runs one goroutine, so waiting for any of that inside a handler would
	// freeze every screen in the program including the one that would have said why. It arrives as a
	// message instead, and the screen stays answerable to escape the whole time.
	case signInStartedMsg:
		return m, m.signInStarted(msg)
	case signInDoneMsg:
		return m, m.signInDone(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch m.mode {
	case modeList:
		m.handleListKey(msg)
	case modeName:
		m.handleTextKey(msg, &m.draftName, (*Model).afterName)
	case modeBaseURL:
		m.handleTextKey(msg, &m.draftBaseURL, (*Model).afterBaseURL)
	case modeModel:
		m.handleTextKey(msg, &m.draftModel, (*Model).afterModel)
	case modeSecret:
		m.handleTextKey(msg, &m.draftSecret, (*Model).afterSecret)
	case modeProvider:
		return m.handleProviderKey(msg)
	case modeSignIn:
		return m.handleSignInKey(msg)
	case modeModelPick:
		m.handleModelPickKey(msg)
	case modeConfirmRemove:
		m.handleConfirmKey(msg)
	}
	return nil
}

func (m *Model) handleListKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "a", "n":
		m.mode = modeName
		m.draftName, m.draftBaseURL, m.draftModel, m.draftSecret = "", "", "", ""
		m.draftProvider = core.ProviderAnthropic
		m.draftRoute = Route{}
		m.providerCursor = 0
		m.status, m.err = "", nil
	case "d", "x", "delete":
		if len(m.keys) > 0 {
			m.mode = modeConfirmRemove
		}
	case "j", "down":
		if m.cursor < len(m.keys)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "r":
		m.reload()
	case "m":
		// Changing the model without re-entering the secret. Somebody fixing a typo in a model id
		// should not have to go and find their API key again.
		if len(m.keys) > 0 {
			m.startModelPick(m.keys[m.cursor])
		}
	case "enter":
		// Choosing which credential the conversation runs on. Without this the list was somewhere to
		// add and remove keys and nowhere to pick one, so with two stored, nothing could run at all.
		if len(m.keys) > 0 {
			m.chosen = m.keys[m.cursor].Ref.Name
			m.storedChoice = false
			m.status = fmt.Sprintf("Selecting %s for this conversation...", m.chosen)
			m.err = nil
		}
	}
}

// startModelPick opens the list of models this credential could be pointed at.
//
// A list before a text field, because typing a model id from memory is how a conversation ends up
// pointed at something with a plausible name that does not exist, and the failure arrives from
// somebody else's gateway as a complaint about a request. The free text field is still there, one
// row down the list, since the catalog is a convenience and never a gate.
func (m *Model) startModelPick(key core.KeyMetadata) {
	m.draftName = key.Ref.Name
	m.draftProvider = key.Ref.Provider
	m.draftBaseURL = key.BaseURL
	m.draftModel = key.Model
	m.editing = true
	m.status, m.err = "", nil

	m.modelChoices = m.offered(key)
	m.modelCursor = 0
	for i, choice := range m.modelChoices {
		// Opened on what it is already set to, so enter is the harmless key rather than the one that
		// silently changes the model to whatever happened to be first.
		if choice.ID == key.Model {
			m.modelCursor = i
			break
		}
	}
	m.mode = modeModelPick
}

// offered is Offered with this screen's error handling.
func (m *Model) offered(key core.KeyMetadata) []catalog.Model {
	offered, err := Offered(m.store, key)
	if err != nil {
		// Worth saying rather than swallowing: a key whose own list could not be read looks
		// identical to one with nothing added, and the second is a normal state.
		m.err = err
	}
	return offered
}

// Offered is everything a credential can be pointed at, in the order it is worth reading: what the
// catalog knows about its provider and endpoint, then what its owner added that the catalog lacks.
//
// A package function, and exported, because the model picker asks this about every key at once and
// two assemblies of one list are two lists that will eventually disagree. A name recorded for a
// model the catalog already has improves that entry rather than adding a second row for it.
//
// The error is returned with the list rather than instead of it: the catalog half is still true when
// the stored half could not be read, and showing it beats showing nothing.
func Offered(store Store, key core.KeyMetadata) ([]catalog.Model, error) {
	offered := catalog.For(key.Ref.Provider, key.BaseURL)

	added, err := store.Models(key.Ref)
	if err != nil {
		return offered, err
	}
	for _, model := range added {
		known := false
		for i, existing := range offered {
			if existing.ID == model.ID {
				known = true
				if model.Name != "" {
					offered[i].Name = model.Name
				}
				break
			}
		}
		if !known {
			offered = append(offered, model)
		}
	}
	return offered, nil
}

// handleModelPickKey moves through the offered models and takes one.
func (m *Model) handleModelPickKey(msg tea.KeyMsg) {
	// One row past the end is the way out of the list. It is a row rather than a separate key
	// because a key that is not on screen is a key nobody finds, and this is the escape the whole
	// catalog depends on being there.
	typeItRow := len(m.modelChoices)

	switch msg.String() {
	case "j", "down":
		if m.modelCursor < typeItRow {
			m.modelCursor++
		}
	case "k", "up":
		if m.modelCursor > 0 {
			m.modelCursor--
		}
	case "esc":
		m.cancelDraft()
	case "enter":
		if m.modelCursor >= typeItRow {
			m.draftModel = ""
			m.mode = modeModel
			return
		}
		m.selectModel(m.modelChoices[m.modelCursor].ID, false)
	}
}

// selectModel records which model this credential talks to, and remembers a typed one.
//
// remember is set only for a model that came from the keyboard, which is what makes the escape
// hatch worth using twice: a model the catalog has never heard of is offered in the list next time
// rather than having to be typed again, exactly.
func (m *Model) selectModel(model string, remember bool) {
	ref := core.KeyRef{Name: m.draftName}
	if remember {
		if err := m.store.AddModel(ref, model, ""); err != nil {
			m.err = err
			return
		}
	}
	if err := m.store.SetModel(ref, model); err != nil {
		m.err = err
		return
	}

	m.editing = false
	m.mode = modeList
	m.status = fmt.Sprintf("%s now talks to %s", m.draftName, model)
	m.err = nil
	m.reload()
}

// handleTextKey edits a text field, shared by every prompt including the secret one, so typed
// input is handled in one place rather than once per field.
func (m *Model) handleTextKey(msg tea.KeyMsg, field *string, commit func(*Model)) {
	switch msg.Type {
	case tea.KeyEnter:
		commit(m)
	case tea.KeyEsc:
		m.cancelDraft()
	case tea.KeyBackspace:
		if runes := []rune(*field); len(runes) > 0 {
			*field = string(runes[:len(runes)-1])
		}
	case tea.KeyRunes:
		*field += string(msg.Runes)
	case tea.KeySpace:
		*field += " "
	}
}

func (m *Model) afterName() {
	name := strings.TrimSpace(m.draftName)
	if err := core.ValidateKeyName(name); err != nil {
		m.err = err
		// If somebody pasted a credential into the name field, do not leave it sitting on screen
		// while they read the rejection. The field is cleared so the value stops being visible the
		// moment we know what it is.
		if core.LooksLikeCredential(name) {
			m.draftName = ""
		}
		return
	}
	m.draftName = name
	m.err = nil
	m.mode = modeProvider
}

func (m *Model) afterBaseURL() {
	url := strings.TrimSpace(m.draftBaseURL)
	if url == "" {
		m.err = fmt.Errorf("provider %q needs an endpoint, for example https://api.moonshot.cn/v1",
			m.draftProvider)
		return
	}
	m.draftBaseURL = url
	m.err = nil
	m.mode = modeModel
}

// afterModel takes the model this credential will talk to.
//
// Asked rather than assumed for every provider except Anthropic, which is the only one Canopy can
// pick a model for. Without it the credential is stored, looks complete in the list, and fails on
// the first message with an error from somebody else's gateway about a request rather than about
// the setting that was never filled in.
func (m *Model) afterModel() {
	model := strings.TrimSpace(m.draftModel)
	if model == "" && m.draftProvider != core.ProviderAnthropic {
		m.err = fmt.Errorf("%q has no default model, so name the one this key should use, "+
			"for example minimaxai/minimax-m2.7", m.draftProvider)
		return
	}
	m.draftModel = model
	m.err = nil

	if m.editing {
		// Remembered as well as selected, since a model typed here is one the catalog does not know
		// and the person has just proved they use it.
		m.selectModel(model, true)
		return
	}
	m.mode = modeSecret
}

func (m *Model) afterSecret() {
	value := strings.TrimSpace(m.draftSecret)
	if value == "" {
		m.err = fmt.Errorf("no value entered")
		return
	}

	meta, err := m.store.Put(core.KeyMetadata{
		Ref:     core.KeyRef{Name: m.draftName, Provider: m.draftProvider},
		BaseURL: m.draftBaseURL,
		Model:   m.draftModel,
	}, core.NewSecret(value))

	// Cleared whether or not the store accepted it. Clearing only on success is the easy mistake,
	// and it leaves a credential sitting in a struct that later code may render.
	m.draftSecret = ""

	if err != nil {
		m.err = err
		return
	}

	// Stored is not chosen, and it used to stop at stored.
	//
	// Somebody with no credentials lands on this screen, walks the wizard, types a key, and arrived
	// back at a list with nothing selected and no sign that a further keystroke was needed. It
	// worked anyway while there was exactly one credential, because the resolver falls back to the
	// only one there is, and broke the moment they added a second: the conversation would answer on
	// whichever the resolver preferred rather than on the one they had just entered.
	//
	// A key somebody has just typed in is the key they want to use. Selecting it here is what makes
	// the wizard end where its user thinks it ended.
	m.chosen = meta.Ref.Name
	m.storedChoice = true

	m.status = fmt.Sprintf("Stored %q for %s (fingerprint %s).", meta.Ref.Name, meta.Ref.Provider,
		meta.Fingerprint)
	m.err = nil
	m.mode = modeList
	m.reload()

	// The cursor follows, so the row the list highlights is the credential that is actually about to
	// answer. A selection the list disagrees with is the same lie at one remove.
	for i, key := range m.keys {
		if key.Ref.Name == m.chosen {
			m.cursor = i
			break
		}
	}
}

// providerRow is one line of the provider step.
//
// Two kinds of row on one list, because from where the person is standing they are answering one
// question: where does this credential come from. Splitting them across two screens would mean
// somebody with a Copilot seat and no API key having to guess that the answer to "which provider"
// is a screen they have not seen yet.
type providerRow struct {
	provider core.Provider
	route    Route
	signIn   bool
}

// providerRows is the provider step's list: what can be pasted, then what can be signed in to.
//
// Pasted first, and not out of seniority. The routes are the rows that change what the wizard does
// next, so putting them last keeps the two familiar rows exactly where they have always been for
// everybody whose credential is still a string.
func (m Model) providerRows() []providerRow {
	rows := make([]providerRow, 0, len(core.AllProviders()))
	for _, provider := range core.AllProviders() {
		rows = append(rows, providerRow{provider: provider})
	}
	if m.signIn == nil {
		return rows
	}
	for _, route := range m.signIn.Routes() {
		rows = append(rows, providerRow{route: route, signIn: true})
	}
	return rows
}

func (m *Model) handleProviderKey(msg tea.KeyMsg) tea.Cmd {
	rows := m.providerRows()
	switch msg.String() {
	case "j", "down":
		if m.providerCursor < len(rows)-1 {
			m.providerCursor++
		}
	case "k", "up":
		if m.providerCursor > 0 {
			m.providerCursor--
		}
	case "esc":
		m.cancelDraft()
	case "enter":
		if m.providerCursor >= len(rows) {
			return nil
		}
		row := rows[m.providerCursor]
		if row.signIn {
			// The branch this task exists for. Nothing between here and a stored credential asks for
			// a value, because there is no value: the vendor holds it and the person proves who they
			// are to the vendor.
			return m.startSignIn(row.route)
		}

		m.draftProvider = row.provider
		if m.draftProvider.RequiresBaseURL() {
			m.mode = modeBaseURL
		} else {
			// Anthropic goes straight to the secret. It is the one provider Canopy can pick a model
			// for, so asking would be asking a question with a correct default, and the list shows
			// what it settled on. Changing it later is m on the list.
			m.mode = modeSecret
		}
	}
	return nil
}

func (m *Model) handleConfirmKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "y", "enter":
		if m.cursor < len(m.keys) {
			name := m.keys[m.cursor].Ref.Name
			if err := m.store.Remove(core.KeyRef{Name: name}); err != nil {
				m.err = err
			} else {
				m.status = fmt.Sprintf("Removed %q.", name)
				m.err = nil
			}
			m.reload()
		}
		m.mode = modeList
	case "n", "esc":
		m.mode = modeList
	}
}

func (m *Model) cancelDraft() {
	m.draftName, m.draftBaseURL, m.draftSecret = "", "", ""
	m.providerCursor = 0
	// Cleared here too, or an abandoned model edit leaves the flag set and the next credential added
	// on a provider that asks for a model gets its model prompt treated as an edit of the last key
	// somebody looked at, storing nothing.
	m.editing = false

	// And the sign-in half of a draft, which is the same rule applied to the step that has no field
	// in it. A route and a device code left behind would put the last vendor's instructions on the
	// next sign-in's screen.
	//
	// The attempt itself is not touched here. Only modeSignIn can be holding one and only its key
	// handler can leave that mode, and it calls abandonAttempt first, because stopping a live
	// attempt needs a command back to the runtime and this returns nothing.
	m.draftRoute = Route{}
	m.prompt = Prompt{}
	m.signingIn = false

	m.mode = modeList
	m.err = nil
	m.status = "Cancelled."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// The credential screen renders through the theme like everything else.
//
// It did not. These were four adaptive colours declared here, which is the same bug that was found
// in internal/tui/styles.go and fixed a few commits ago: the values duplicated the old default
// palette, so this screen stayed in those colours whatever theme was selected. Finding the second
// copy is the argument for the rule internal/tui/theme opens with, since nothing about the first
// one would have led anybody here.
//
// Resolved at render time behind a Render method, so no call site changed and none of these can
// capture whichever palette happened to be current when the package was initialised.
type themed func() lipgloss.Style

func (t themed) Render(strs ...string) string { return t().Render(strs...) }

var (
	styleTitle  = themed(func() lipgloss.Style { return theme.Current().Title })
	styleMuted  = themed(func() lipgloss.Style { return theme.Current().Muted })
	styleErr    = themed(func() lipgloss.Style { return theme.Current().Danger })
	styleOK     = themed(func() lipgloss.Style { return theme.Current().Success })
	styleWarn   = themed(func() lipgloss.Style { return theme.Current().Warning })
	styleSelect = themed(func() lipgloss.Style { return theme.Current().Selected })
)
