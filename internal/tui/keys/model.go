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

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Store is the part of the credential store this screen needs.
//
// Narrow on purpose. The screen can list, add and remove, and there is deliberately no method here
// that returns a secret value, so no amount of mistakes in this package can render one.
type Store interface {
	List() ([]core.KeyMetadata, error)
	Put(meta core.KeyMetadata, secret core.Secret) (core.KeyMetadata, error)
	Remove(ref core.KeyRef) error
	BackendName() string
	UsingInsecureBackend() bool
}

type mode int

const (
	modeList mode = iota
	modeName
	modeProvider
	modeBaseURL
	modeSecret
	modeConfirmRemove
)

// Model is the credential screen.
type Model struct {
	store Store

	mode   mode
	keys   []core.KeyMetadata
	cursor int

	// draft holds the credential being added.
	draftName     string
	draftProvider core.Provider
	draftBaseURL  string

	// draftSecret is the one place in this design where a credential exists as a plain string, and
	// it is unavoidable: the user is typing it. It is cleared the moment it reaches the store, and
	// it is never rendered, only its length is.
	draftSecret string

	providerCursor int

	status string
	err    error

	width int
}

// New builds the credential screen.
func New(store Store) Model {
	m := Model{store: store, draftProvider: core.ProviderAnthropic, width: 80}
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
}

// IsEmpty reports whether no credentials are stored, which is what makes this the first thing a
// new user sees.
func (m Model) IsEmpty() bool { return len(m.keys) == 0 }

// Adding reports whether the screen is mid way through adding a credential, so a parent model
// knows not to steal keystrokes.
func (m Model) Adding() bool { return m.mode != modeList && m.mode != modeConfirmRemove }

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
	case modeSecret:
		m.handleTextKey(msg, &m.draftSecret, (*Model).afterSecret)
	case modeProvider:
		m.handleProviderKey(msg)
	case modeConfirmRemove:
		m.handleConfirmKey(msg)
	}
	return nil
}

func (m *Model) handleListKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "a", "n":
		m.mode = modeName
		m.draftName, m.draftBaseURL, m.draftSecret = "", "", ""
		m.draftProvider = core.ProviderAnthropic
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
	}
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
	}, core.NewSecret(value))

	// Cleared whether or not the store accepted it. Clearing only on success is the easy mistake,
	// and it leaves a credential sitting in a struct that later code may render.
	m.draftSecret = ""

	if err != nil {
		m.err = err
		return
	}

	m.status = fmt.Sprintf("Stored %q for %s (fingerprint %s).",
		meta.Ref.Name, meta.Ref.Provider, meta.Fingerprint)
	m.err = nil
	m.mode = modeList
	m.reload()
}

func (m *Model) handleProviderKey(msg tea.KeyMsg) {
	providers := core.AllProviders()
	switch msg.String() {
	case "j", "down":
		if m.providerCursor < len(providers)-1 {
			m.providerCursor++
		}
	case "k", "up":
		if m.providerCursor > 0 {
			m.providerCursor--
		}
	case "esc":
		m.cancelDraft()
	case "enter":
		m.draftProvider = providers[m.providerCursor]
		if m.draftProvider.RequiresBaseURL() {
			m.mode = modeBaseURL
		} else {
			m.mode = modeSecret
		}
	}
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

var (
	styleTitle  = lipgloss.NewStyle().Bold(true)
	styleMuted  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"})
	styleErr    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"})
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"})
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"})
	styleSelect = lipgloss.NewStyle().Bold(true)
)
