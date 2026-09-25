package session

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A picture sent with a message reaches the provider with it, and stays with it for the turns after.
func TestAPictureTravelsWithItsMessage(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("I see it")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()
	session := e.Create("claude", "claude-opus-5")
	picture := core.Image{MediaType: "image/png", Data: []byte("PNG")}
	turnID, err := e.SendWithImages(session.ID, "what is wrong", []core.Image{picture})
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, session.ID, turnID)
	second, err := e.Send(session.ID, "and now")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, session.ID, second)
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.history) == 0 || len(client.history[0].Images) != 1 || string(client.history[0].Images[0].Data) != "PNG" {
		t.Fatalf("the second request's history is %+v", client.history)
	}
	if last := client.history[len(client.history)-1]; len(last.Images) != 0 {
		t.Fatal("a message with no picture was sent with one")
	}
}
