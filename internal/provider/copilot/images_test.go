package copilot

import (
	"context"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func TestCopilotRefusesPictures(t *testing.T) {
	req := core.Request{Model: "m", Messages: []core.Message{{Role: core.RoleUser, Text: "look",
		Images: []core.Image{{MediaType: "image/png", Data: []byte("x")}}}}}
	if _, err := (&Client{name: "copilot"}).Stream(context.Background(), req); err == nil ||
		!strings.Contains(err.Error(), "cannot take pictures") {
		t.Fatalf("%v", err)
	}
}
