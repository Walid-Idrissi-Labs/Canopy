package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const planted = "sk-ant-api03-PLANTED-SECRET-VALUE-DO-NOT-LEAK"

// The A1-01 acceptance criterion, and the reason this type exists.
//
// A leaked credential is the one bug in this project that cannot be fixed after it is found, so
// it is designed out rather than tested for afterwards. Every route Go offers for turning a value
// into text is checked here, because the leak that actually happens is a debugging line somebody
// wrote at speed and then committed.
func TestSecretNeverPrintsItself(t *testing.T) {
	secret := NewSecret(planted)

	renderings := map[string]string{
		"%v":        fmt.Sprintf("%v", secret),
		"%s":        fmt.Sprintf("%s", secret),
		"%q":        fmt.Sprintf("%q", secret),
		"%#v":       fmt.Sprintf("%#v", secret),
		"%+v":       fmt.Sprintf("%+v", secret),
		"%x":        fmt.Sprintf("%x", secret),
		"%d":        fmt.Sprintf("%d", secret),
		"String()":  secret.String(),
		"GoString":  secret.GoString(),
		"Sprint":    fmt.Sprint(secret),
		"Sprintln":  fmt.Sprintln(secret),
		"in-struct": fmt.Sprintf("%v", struct{ Key Secret }{secret}),
		"in-slice":  fmt.Sprintf("%v", []Secret{secret}),
		"in-map":    fmt.Sprintf("%v", map[string]Secret{"k": secret}),
		"pointer":   fmt.Sprintf("%v", &secret),
		"error":     fmt.Errorf("failed with %v", secret).Error(),
	}

	for how, got := range renderings {
		if strings.Contains(got, planted) {
			t.Errorf("%s leaked the secret: %s", how, got)
		}
		if !strings.Contains(got, Redacted) {
			t.Errorf("%s did not redact, got %q", how, got)
		}
	}
}

func TestSecretNeverSerialises(t *testing.T) {
	secret := NewSecret(planted)

	encodings := map[string]any{
		"direct": secret,
		"in-struct": struct {
			Name string
			Key  Secret
		}{"claude", secret},
		"in-slice": []Secret{secret},
		"in-map":   map[string]Secret{"claude": secret},
		"nested": struct {
			Inner struct{ Key Secret }
		}{struct{ Key Secret }{secret}},
	}

	for how, value := range encodings {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Errorf("%s: marshal failed: %v", how, err)
			continue
		}
		if strings.Contains(string(encoded), planted) {
			t.Errorf("%s leaked the secret through JSON: %s", how, encoded)
		}
	}
}

// Refusing to unmarshal removes the supported path for putting a credential in a config file that
// somebody then commits.
func TestSecretRefusesToLoadFromJSON(t *testing.T) {
	var s Secret
	if err := json.Unmarshal([]byte(`"sk-whatever"`), &s); err == nil {
		t.Fatal("unmarshalling a secret should fail")
	}
	if !s.IsZero() {
		t.Error("a refused unmarshal must not leave a value behind")
	}

	var wrapper struct {
		Key Secret `json:"key"`
	}
	if err := json.Unmarshal([]byte(`{"key":"sk-whatever"}`), &wrapper); err == nil {
		t.Error("unmarshalling a secret inside a struct should fail too")
	}
}

func TestSecretReveal(t *testing.T) {
	if got := NewSecret(planted).Reveal(); got != planted {
		t.Errorf("Reveal() = %q, want the original value", got)
	}
	if !NewSecret("").IsZero() {
		t.Error("an empty secret is zero")
	}
	if NewSecret("x").IsZero() {
		t.Error("a non-empty secret is not zero")
	}
}

func TestSecretFingerprint(t *testing.T) {
	a := NewSecret(planted)
	b := NewSecret(planted + "different")

	if a.Fingerprint() == "" {
		t.Fatal("a set secret should have a fingerprint")
	}
	if a.Fingerprint() != NewSecret(planted).Fingerprint() {
		t.Error("the same value should fingerprint the same")
	}
	if a.Fingerprint() == b.Fingerprint() {
		t.Error("different values should fingerprint differently")
	}
	if NewSecret("").Fingerprint() != "" {
		t.Error("an empty secret has no fingerprint")
	}

	// The point of a fingerprint is telling keys apart without showing either, so it must not
	// contain the thing it identifies, and must be far too short to work backwards from.
	if strings.Contains(a.Fingerprint(), planted) {
		t.Error("the fingerprint contains the secret")
	}
	if len(a.Fingerprint()) != 12 {
		t.Errorf("fingerprint length = %d, want 12", len(a.Fingerprint()))
	}
}

// KeyRef is the type that travels: into profiles, agents, events, snapshots and transcripts. It is
// only safe for it to go everywhere because it has nowhere to put a credential. This test fails if
// somebody adds a field that could hold one.
func TestKeyRefCannotHoldASecret(t *testing.T) {
	typ := reflect.TypeOf(KeyRef{})

	allowed := map[string]reflect.Kind{
		"Name":     reflect.String,
		"Provider": reflect.String,
	}

	if typ.NumField() != len(allowed) {
		t.Fatalf("KeyRef has %d fields, expected %d. A new field on the type that travels "+
			"everywhere needs deliberate review, since it is only safe to pass around because it "+
			"cannot carry a credential.", typ.NumField(), len(allowed))
	}

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		kind, ok := allowed[field.Name]
		if !ok {
			t.Errorf("unexpected field %q on KeyRef", field.Name)
			continue
		}
		if field.Type.Kind() != kind {
			t.Errorf("field %q is %s, want %s", field.Name, field.Type.Kind(), kind)
		}
		if field.Type == reflect.TypeOf(Secret{}) {
			t.Errorf("field %q is a Secret, which defeats the point of KeyRef", field.Name)
		}
	}
}

// The same check for everything else that gets published to the UI or written to disk.
func TestPublishedTypesCarryNoSecrets(t *testing.T) {
	secretType := reflect.TypeOf(Secret{})

	for _, value := range []any{
		KeyRef{}, KeyMetadata{}, AgentProfile{},
		WorkspaceSnapshot{}, ProjectSnapshot{}, TestRun{}, ServiceHealth{}, Event{},
	} {
		typ := reflect.TypeOf(value)
		var walk func(reflect.Type, string, int)
		walk = func(t2 reflect.Type, path string, depth int) {
			if depth > 4 || t2 == nil {
				return
			}
			switch t2.Kind() {
			case reflect.Pointer, reflect.Slice, reflect.Array:
				walk(t2.Elem(), path+"[]", depth+1)
			case reflect.Struct:
				if t2 == secretType {
					t.Errorf("%s reaches a Secret at %s, and this type is published", typ, path)
					return
				}
				for i := 0; i < t2.NumField(); i++ {
					f := t2.Field(i)
					walk(f.Type, path+"."+f.Name, depth+1)
				}
			}
		}
		walk(typ, typ.Name(), 0)
	}
}
