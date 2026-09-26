package keys

// Two things a credential needs beyond being stored: finding out whether the provider takes it,
// before the first message rather than by it, and a price for an endpoint Canopy has no rate for,
// entered here rather than through a CLI command nobody is told about.

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// CheckResult is what asking a credential's provider found.
type CheckResult struct {
	// Accepted is the provider answering with the credential, and Refused is it saying no to it.
	// Neither is a check that could not be made, and Note says why.
	Accepted, Refused bool
	Note              string
}

// Check asks a credential's provider whether it takes the credential, by name. It is made off the
// update loop and must cost nothing: a model list, never a message.
type Check func(name string) CheckResult

// SetCheck attaches the live check; without one, a stored credential is only stored.
func (m *Model) SetCheck(check Check) { m.check = check }

// checkDoneMsg carries a check's answer back to the screen.
type checkDoneMsg struct {
	name   string
	result CheckResult
}

// startCheck asks about a credential, when there is a check to ask with.
func (m *Model) startCheck(name string) tea.Cmd {
	if m.check == nil || name == "" {
		return nil
	}
	m.checking = name
	check := m.check
	return func() tea.Msg { return checkDoneMsg{name: name, result: check(name)} }
}

// checkDone reports the answer, naming the credential either way.
func (m *Model) checkDone(msg checkDoneMsg) {
	if msg.name != m.checking {
		return
	}
	m.checking = ""
	switch {
	case msg.result.Accepted:
		m.status = strings.TrimSpace(m.status + " The provider accepted " + msg.name + ".")
	case msg.result.Refused:
		m.status = ""
		m.err = fmt.Errorf("the provider refused %s: %s. d removes it so it can be added again",
			msg.name, msg.result.Note)
	default:
		m.status = strings.TrimSpace(m.status + " " + msg.name + " was not checked: " + msg.result.Note + ".")
	}
}

// startRate opens the price field on a credential.
func (m *Model) startRate(key core.KeyMetadata) {
	m.ratingKey = key.Ref
	m.draftRate = ""
	if !key.Rate.IsZero() {
		m.draftRate = strconv.FormatFloat(key.Rate.InputPerMTok, 'f', -1, 64) + " " +
			strconv.FormatFloat(key.Rate.OutputPerMTok, 'f', -1, 64)
		if key.Rate.CacheReadPerMTok > 0 {
			m.draftRate += " " + strconv.FormatFloat(key.Rate.CacheReadPerMTok, 'f', -1, 64)
		}
	}
	m.status, m.err = "", nil
	m.mode = modeRate
}

// afterRate records the price typed: input and output dollars per million tokens, and optionally
// cached input; nothing at all forgets it.
func (m *Model) afterRate() {
	fields := strings.Fields(strings.ReplaceAll(m.draftRate, "$", ""))
	var rate core.KeyRate
	switch len(fields) {
	case 0:
	case 2, 3:
		values := make([]float64, len(fields))
		for i, field := range fields {
			v, err := strconv.ParseFloat(field, 64)
			if err != nil {
				m.err = fmt.Errorf("%q is not a number of dollars", field)
				return
			}
			values[i] = v
		}
		rate = core.KeyRate{InputPerMTok: values[0], OutputPerMTok: values[1]}
		if len(values) == 3 {
			rate.CacheReadPerMTok = values[2]
		}
		if err := rate.Validate(); err != nil {
			m.err = err
			return
		}
	default:
		m.err = fmt.Errorf("two or three amounts: input and output per million tokens, and cached input if it differs")
		return
	}
	if err := m.store.SetRate(m.ratingKey, rate); err != nil {
		m.err = err
		return
	}
	if rate.IsZero() {
		m.status = "Forgot the rate for " + m.ratingKey.Name + "; its turns read as unpriced, not as free."
	} else {
		m.status = fmt.Sprintf("Recorded $%g in and $%g out per million tokens for %s, shown as your own rate.",
			rate.InputPerMTok, rate.OutputPerMTok, m.ratingKey.Name)
	}
	m.err = nil
	m.mode = modeList
	m.reload()
}
