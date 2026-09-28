package core

import (
	"strings"
	"testing"
)

// The last few messages with pictures keep them; older ones say one was there; the conversation
// given is left alone.
func TestOnlyRecentPicturesAreSentAgain(t *testing.T) {
	var messages []Message
	for i := 0; i < 5; i++ {
		messages = append(messages, Message{Role: RoleUser, Text: "look", Images: []Image{{MediaType: "image/png", Data: []byte("x")}}},
			Message{Role: RoleAssistant, Text: "ok"})
	}
	out := KeepRecentPictures(messages)
	with := 0
	for i, m := range out {
		if len(m.Images) > 0 {
			with++
			if i < 4 {
				t.Fatalf("message %d kept its picture", i)
			}
		}
	}
	if with != PicturesKept || !strings.Contains(out[0].Text, "no longer attached") || len(messages[0].Images) != 1 {
		t.Fatalf("kept %d, first says %q, original changed %v", with, out[0].Text, len(messages[0].Images) != 1)
	}
	huge := []Message{{Role: RoleUser, Images: []Image{{Data: make([]byte, PictureBytesKept)}}},
		{Role: RoleUser, Images: []Image{{Data: make([]byte, 10)}}}}
	if got := KeepRecentPictures(huge); len(got[0].Images) != 0 || len(got[1].Images) != 1 {
		t.Fatal("pictures past the byte budget were kept")
	}
	req := Request{Messages: []Message{{Images: []Image{{}}}, {Text: "and now"}}}
	if req.HasImages() {
		t.Fatal("a picture earlier in the conversation counted as one in the message being sent")
	}
}
