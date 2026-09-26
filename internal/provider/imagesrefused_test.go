package provider_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/acp"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
)

// A route that cannot pass a picture on says so rather than sending the words without it.
func TestDelegatedRoutesRefusePictures(t *testing.T) {
	req := core.Request{Model: "m", Messages: []core.Message{{Role: core.RoleUser, Text: "look",
		Images: []core.Image{{MediaType: "image/png", Data: []byte("x")}}}}}
	for name, client := range map[string]core.ProviderClient{
		"claude code": acp.New(acp.Installation{}), "codex": codex.New(codex.Installation{}),
	} {
		_, err := client.Stream(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "cannot take pictures") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
