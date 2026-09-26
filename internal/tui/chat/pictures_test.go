package chat_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/images"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// A picture dropped on the terminal, which types its path, goes with the message.
func TestAPictureNamedInAMessageIsAttached(t *testing.T) {
	dir := t.TempDir()
	var data bytes.Buffer
	_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	shot := filepath.Join(dir, "Screen Shot.png")
	if err := os.WriteFile(shot, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	m.SetPictures(images.FindPaths, images.LoadAll)
	m, _ = m.Update(tea.PasteMsg{Content: "why does this overflow " + strings.ReplaceAll(shot, " ", `\ `)})
	m, cmd := m.Update(keyCode(tea.KeyEnter))
	if cmd == nil || len(engine.sent) != 0 {
		t.Fatal("the pictures were not read off the update loop")
	}
	// A second enter while they are read starts nothing more.
	if _, again := m.Update(keyCode(tea.KeyEnter)); again != nil {
		t.Fatal("a second enter started a second read, and would send twice")
	}
	m, _ = m.Update(cmd())
	if len(engine.sent) != 1 || len(engine.pictures) != 1 || engine.pictures[0].MediaType != "image/png" {
		t.Fatalf("sent %v with %d pictures", engine.sent, len(engine.pictures))
	}
	if !strings.Contains(m.Notice(), "a picture") {
		t.Fatalf("notice %q", m.Notice())
	}

	// A picture that cannot be read stops the message rather than sending the words alone.
	broken := filepath.Join(dir, "broken.png")
	if err := os.WriteFile(broken, []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ = m.Update(tea.PasteMsg{Content: "and " + broken})
	m, cmd = m.Update(keyCode(tea.KeyEnter))
	m, _ = m.Update(cmd())
	if len(engine.sent) != 1 || m.InputValue() == "" || !strings.Contains(m.Error(), "nothing was sent") {
		t.Fatalf("sent %v, box %q, error %q", engine.sent, m.InputValue(), m.Error())
	}
}

// Pictures read for a conversation left meanwhile are not sent into the new one, and it is said.
func TestPicturesReadAfterASwitchAreNotSentElsewhere(t *testing.T) {
	dir := t.TempDir()
	var data bytes.Buffer
	_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	shot := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(shot, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	m.SetPictures(images.FindPaths, images.LoadAll)
	m, _ = m.Update(tea.PasteMsg{Content: "look " + shot})
	m, cmd := m.Update(keyCode(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("nothing was read")
	}
	m.SetSession("s2", "other")
	m, _ = m.Update(cmd())
	if len(engine.sent) != 0 {
		t.Fatalf("sent %v into the other conversation", engine.sent)
	}
	if !strings.Contains(m.Error(), "not sent") {
		t.Fatalf("error %q", m.Error())
	}
}

// The transcript says a message carried pictures.
func TestTheTranscriptSaysAMessageHadPictures(t *testing.T) {
	session := core.Session{ID: "s1", Turns: []core.Turn{{ID: "t1", State: core.TurnComplete, Text: "ok",
		Request: core.Message{Role: core.RoleUser, Text: "look", Images: []core.Image{{}, {}}}}}}
	if got := plain(strings.Join(chat.Transcript(session, 60, ".", nil), "\n")); !strings.Contains(got, "with 2 pictures") {
		t.Fatalf("%s", got)
	}
}

// Agents' reports that went with a message are shown as having gone with it.
func TestTheTranscriptSaysAMessageCarriedReports(t *testing.T) {
	one := turn("t1", "what did they find", "both pass", core.TurnComplete)
	one.Request.Reports = []string{"agent a", "agent b"}
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{one}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	if !strings.Contains(plain(m.Body()), "with the reports of 2 agents") {
		t.Fatalf("no reports line:\n%s", plain(m.Body()))
	}
}
