package main

import (
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// Always on a dispatch question is kept, and the next dispatch in that conversation is not asked.
func TestAlwaysStartingAgentsIsHonoured(t *testing.T) {
	engine := session.New(nil)
	t.Cleanup(engine.Close)
	confirm := dispatchConfirm(engine, "s1")
	done := make(chan bool, 1)
	go func() { done <- confirm(session.Confirmation{}) }()
	for {
		if waiting := engine.PendingAll(); len(waiting) == 1 {
			if waiting[0].Decision.Scope.String() == "" {
				t.Fatal("the question offers always with nothing after it")
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	engine.Answer("s1", true, true)
	if !<-done {
		t.Fatal("the approval was lost")
	}
	answered := make(chan bool, 1)
	go func() { answered <- confirm(session.Confirmation{}) }()
	select {
	case ok := <-answered:
		if !ok {
			t.Fatal("a dispatch after always was refused")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a dispatch after always was asked again")
	}
}
