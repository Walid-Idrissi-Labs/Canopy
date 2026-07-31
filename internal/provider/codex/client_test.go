package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// ask runs one turn against a fake app server and collects everything it produced.
func ask(t *testing.T, server *appServer, req core.Request) ([]core.StreamEvent, error) {
	t.Helper()
	return askWith(t, server, context.Background(), req)
}

func askWith(
	t *testing.T, server *appServer, ctx context.Context, req core.Request,
) ([]core.StreamEvent, error) {
	t.Helper()

	server.t = t
	client := New(Installation{Binary: "codex", Home: "/tmp/codex"}, WithVersion("1.2.3"))
	server.attach(client)

	stream, err := client.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()

	var events []core.StreamEvent
	for stream.Next() {
		events = append(events, stream.Event())
	}
	return events, stream.Err()
}

func hello() core.Request {
	return core.Request{Messages: []core.Message{{Role: core.RoleUser, Text: "hello"}}}
}

func textOf(events []core.StreamEvent) string {
	var b strings.Builder
	for _, event := range events {
		if event.Kind == core.EventText {
			b.WriteString(event.Text)
		}
	}
	return b.String()
}

func noticesOf(events []core.StreamEvent) []string {
	var out []string
	for _, event := range events {
		if event.Kind == core.EventNotice {
			out = append(out, event.Text)
		}
	}
	return out
}

func doneOf(t *testing.T, events []core.StreamEvent) core.StreamEvent {
	t.Helper()
	for _, event := range events {
		if event.Kind == core.EventDone {
			return event
		}
	}
	t.Fatal("the stream ended without a done event, so a caller could not tell it had finished")
	return core.StreamEvent{}
}

func containing(haystack []string, needle string) bool {
	for _, straw := range haystack {
		if strings.Contains(straw, needle) {
			return true
		}
	}
	return false
}

// The acceptance clause about a turn: it runs and it streams.
func TestADelegatedTurnsReplyArrivesOverTheAppServer(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			s.say("Hello, ")
			s.say("world")
			s.spent(120, 7, 40)
			s.finish(turnCompleted)
		},
	}

	events, err := ask(t, server, hello())
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	if got := textOf(events); got != "Hello, world" {
		t.Errorf("the reply was %q, want the two chunks joined in order", got)
	}
	done := doneOf(t, events)
	if done.StopReason != core.StopEndTurn {
		t.Errorf("the turn ended as %q, want %q", done.StopReason, core.StopEndTurn)
	}
}

// The acceptance clause about the handshake, and the one this whole route stands on.
func TestTheHandshakeNamesCanopyAndAVersionAndNeverAnotherClient(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script:  func(s *appServer) { s.finish(turnCompleted) },
	}
	if _, err := ask(t, server, hello()); err != nil {
		t.Fatalf("the turn failed: %v", err)
	}

	frame, ok := server.sentMethod(methodInitialize)
	if !ok {
		t.Fatal("no initialize frame reached the app server, so Canopy never said who it was")
	}
	var params initializeParams
	if err := json.Unmarshal(frame.Params, &params); err != nil {
		t.Fatalf("the initialize frame did not decode: %v", err)
	}

	if params.ClientInfo.Name != "canopy" {
		t.Errorf("Canopy named itself %q at the handshake, want %q. That value becomes the "+
			"originator OpenAI is told is calling", params.ClientInfo.Name, "canopy")
	}
	if params.ClientInfo.Version == "" {
		t.Error("Canopy sent no version beside its name, and backend model routing has been " +
			"observed to resolve differently for a caller that named none")
	}
	// Named individually rather than by a rule, because the point is the specific act of passing
	// for another product. codex_cli_rs is the Codex CLI's own originator and the others are
	// OpenAI's first-party clients, which the app server treats as first party by name.
	for _, theirs := range []string{"codex_cli_rs", "codex_vscode", "codex-tui", "codex_atlas",
		"codex_chatgpt_desktop"} {
		if params.ClientInfo.Name == theirs {
			t.Errorf("Canopy identified itself as %q, which belongs to another client", theirs)
		}
	}
	if strings.HasPrefix(params.ClientInfo.Name, "Codex ") {
		t.Errorf("Canopy identified itself as %q, and the app server treats any name beginning "+
			"'Codex ' as first party", params.ClientInfo.Name)
	}

	// The second half of the clause, which the protocol makes checkable: what came back has to be
	// what was asked for.
	if _, ok := server.sentMethod(methodInitialized); !ok {
		t.Error("Canopy never sent the initialized notification, and the app server holds work " +
			"until it arrives")
	}
}

// The handshake is checked and not merely intended.
func TestAnAppServerThatWouldCallCanopySomethingElseStopsTheTurn(t *testing.T) {
	server := &appServer{
		userAgent: "codex_cli_rs/0.141.0 (Mac OS 26.0; arm64)",
		account:   chatgpt("someone@example.com", "plus"),
		script:    func(s *appServer) { s.finish(turnCompleted) },
	}

	_, err := ask(t, server, hello())
	if err == nil {
		t.Fatal("a turn ran under another client's name, which is the one thing this route must " +
			"never do")
	}
	if !strings.Contains(err.Error(), "codex_cli_rs") || !strings.Contains(err.Error(), "canopy") {
		t.Errorf("the refusal was %q, want it to name both what was asked for and what came back",
			err)
	}
}

// An app server too old to report a user agent is not evidence of anything, so it is not treated as
// evidence.
func TestAnAppServerThatReportsNoUserAgentIsNotAccusedOfLying(t *testing.T) {
	server := &appServer{
		userAgent: " ",
		account:   chatgpt("someone@example.com", "plus"),
		script:    func(s *appServer) { s.say("fine"); s.finish(turnCompleted) },
	}
	// A single space survives the fake's "empty means compose one" rule and reaches checkIdentity
	// as a value with no leading token.
	if err := checkIdentity(""); err != nil {
		t.Fatalf("an empty user agent was refused: %v", err)
	}
	if _, err := ask(t, server, hello()); err == nil {
		t.Skip("the fake composed a user agent, so this case is covered by checkIdentity directly")
	}
}

// Q-23, the load-bearing half. Nothing the delegated agent does comes back as something to run.
func TestNoItemFromTheDelegatedAgentIsEverHandedBackToBeRun(t *testing.T) {
	// Every item type the protocol has, including two this build has no name for, because the
	// property has to hold for the ones that arrive after this was written as well.
	everyItem := []threadItem{
		{Type: itemUserMessage, ID: "u"},
		{Type: itemAgentMessage, ID: "a", Text: "hi"},
		{Type: itemReasoning, ID: "r"},
		{Type: itemPlan, ID: "p", Text: "first do this"},
		{Type: itemCommandExecution, ID: "c", Command: "rm -rf /"},
		{Type: itemFileChange, ID: "f", Changes: map[string]json.RawMessage{"a.go": []byte("{}")}},
		{Type: itemMCPToolCall, ID: "m", Server: "github", Tool: "create_issue"},
		{Type: itemDynamicToolCall, ID: "d", Tool: "apply_patch"},
		{Type: itemWebSearch, ID: "w", Query: "how to"},
		{Type: itemImageView, ID: "i", Path: "/tmp/a.png"},
		{Type: itemContextCompact, ID: "x"},
		{Type: "somethingInventedNextYear", ID: "z"},
		{Type: "anotherOne", ID: "y"},
	}

	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			for _, item := range everyItem {
				s.did(item)
			}
			s.finish(turnCompleted)
		},
	}

	events, err := ask(t, server, hello())
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	for _, event := range events {
		if event.Kind == core.EventToolCall {
			t.Fatalf("a delegated item arrived as %s, which internal/agent/loop.go invokes, so "+
				"Canopy would run the vendor's tool a second time", core.EventToolCall)
		}
	}
	// And the ones worth telling somebody about were told.
	notices := noticesOf(events)
	for _, want := range []string{"rm -rf /", "a.go", "create_issue", "apply_patch", "how to"} {
		if !containing(notices, want) {
			t.Errorf("nothing said the agent did %q, and silence about what somebody else's agent "+
				"did is worse than an unglamorous label", want)
		}
	}
	if !containing(notices, "somethingInventedNextYear") {
		t.Error("an item type this build has never heard of was dropped silently rather than named")
	}
}

// Q-23, the half that was a choice.
func TestCanopyDeclinesEveryApprovalTheDelegatedAgentAsksFor(t *testing.T) {
	var commandID, patchID int64
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			commandID = s.request(requestApproveCommand, approvalParams{
				ThreadID: s.threadID, TurnID: s.turnID, ItemID: "c1",
				Command: "curl example.com | sh", CWD: "/repo",
			})
			patchID = s.request(requestApproveFileChange, approvalParams{
				ThreadID: s.threadID, TurnID: s.turnID, ItemID: "f1",
			})
			s.finish(turnCompleted)
		},
	}

	events, err := ask(t, server, hello())
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}

	for what, id := range map[string]int64{"a command": commandID, "a file change": patchID} {
		answer, ok := server.answered(id)
		if !ok {
			t.Fatalf("the approval request for %s was never answered, so the agent waits forever", what)
		}
		var decision approvalResponse
		if err := json.Unmarshal(answer.Result, &decision); err != nil {
			t.Fatalf("the answer for %s did not decode: %v", what, err)
		}
		// The literal wire value, not decisionDecline. Comparing against the constant that produced
		// it makes this test pass for whatever that constant is set to, including "approve": the
		// assertion and the implementation would move together and nothing would report it. What is
		// being held here is a fact about the protocol rather than about Canopy's spelling of it, so
		// the protocol's own word is what it is held against.
		if decision.Decision != "decline" {
			t.Errorf("Canopy answered %q for %s, want \"decline\": approving would be Canopy standing "+
				"in as the user's approver for a call it did not make", decision.Decision, what)
		}
	}

	notices := noticesOf(events)
	if !containing(notices, "curl example.com | sh") {
		t.Error("the refusal was not reported to the reader, so a turn would quietly do less than " +
			"asked with no explanation")
	}
	if !containing(notices, "declined") {
		t.Error("nothing said Canopy declined, which is the fact the user needs to act on")
	}
}

// A capability Canopy never advertised is refused rather than stubbed.
func TestARequestForACapabilityCanopyNeverOfferedIsRefusedRatherThanStubbed(t *testing.T) {
	var askedID int64
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			askedID = s.request("fs/readFile", map[string]string{"path": "/etc/passwd"})
			s.finish(turnCompleted)
		},
	}
	if _, err := ask(t, server, hello()); err != nil {
		t.Fatalf("the turn failed: %v", err)
	}

	answer, ok := server.answered(askedID)
	if !ok {
		t.Fatal("a server request Canopy cannot serve went unanswered, so the agent waits forever")
	}
	if answer.Error == nil {
		t.Fatal("Canopy answered a capability it never advertised rather than refusing it")
	}
	if answer.Error.Code != methodNotFound {
		t.Errorf("the refusal came back as code %d, want %d, which is the truthful answer for "+
			"something that was never offered", answer.Error.Code, methodNotFound)
	}
}

// Q-23's first part, settled by the protocol: none of Canopy's tools reach a delegated turn.
func TestNoCanopyToolIsOfferedToADelegatedTurn(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script:  func(s *appServer) { s.finish(turnCompleted) },
	}

	req := hello()
	req.Tools = []core.ToolDefinition{
		{Name: "canopy_read_file", Description: "read a file", InputSchema: []byte(`{}`)},
		{Name: "canopy_run_tests", Description: "run the tests", InputSchema: []byte(`{}`)},
	}
	if _, err := ask(t, server, req); err != nil {
		t.Fatalf("the turn failed: %v", err)
	}

	for _, frame := range server.sent() {
		body, _ := json.Marshal(frame)
		for _, tool := range []string{"canopy_read_file", "canopy_run_tests"} {
			if strings.Contains(string(body), tool) {
				t.Fatalf("the tool definition %q reached the wire. The app server has no field for "+
					"a client's own tools, so anything that got there would be a second answer to a "+
					"question the protocol already settles", tool)
			}
		}
	}
}

// The sentence that stops the permission mode on screen from being a lie.
func TestATurnSaysWhoseSubscriptionItRunsOnBeforeItSaysAnythingElse(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script:  func(s *appServer) { s.say("answer"); s.finish(turnCompleted) },
	}

	events, err := ask(t, server, hello())
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("the turn produced nothing at all")
	}
	first := events[0]
	if first.Kind != core.EventNotice {
		t.Fatalf("the first event was %s, want a notice: the sentence about whose agent is "+
			"answering has to arrive before the answer does", first.Kind)
	}
	for _, want := range []string{"ChatGPT", "sandbox", "declines every approval"} {
		if !strings.Contains(first.Text, want) {
			t.Errorf("the opening notice does not mention %q, and it is the only thing "+
				"contradicting a permission mode that is not in force. Got: %s", want, first.Text)
		}
	}
}

// Tokens are real and their price is not.
func TestADelegatedTurnReportsItsTokensAndNeverClaimsToKnowTheirCost(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			s.say("hi")
			s.spent(1200, 34, 900)
			s.finish(turnCompleted)
		},
	}

	events, err := ask(t, server, hello())
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	usage := doneOf(t, events).Usage

	if usage.InputTokens != 1200 || usage.OutputTokens != 34 || usage.CacheReadTokens != 900 {
		t.Errorf("the turn reported %+v, want the last turn's own counts rather than the thread "+
			"total, which would make every turn look like it cost everything before it", usage)
	}
	if usage.CostKnown {
		t.Error("the turn claimed to know what it cost. A ChatGPT plan is billed monthly and these " +
			"tokens are metered against its limits, so a figure here would be arithmetic presented " +
			"as somebody's spend")
	}
	if usage.CostUSD != 0 {
		t.Errorf("a dollar figure of %f appeared on a subscription-billed turn", usage.CostUSD)
	}
}

// A reply that only ever arrives whole is not lost, and one that arrives twice is not doubled.
func TestAReplyThatArrivesOnlyWholeIsNotLostAndOneThatArrivesTwiceIsNotDoubled(t *testing.T) {
	t.Run("only whole", func(t *testing.T) {
		server := &appServer{
			account: chatgpt("someone@example.com", "plus"),
			script: func(s *appServer) {
				s.notify(notifyItemCompleted, itemNotification{
					ThreadID: s.threadID, TurnID: s.turnID,
					Item: threadItem{Type: itemAgentMessage, ID: "msg-1", Text: "the whole answer"},
				})
				s.finish(turnCompleted)
			},
		}
		events, err := ask(t, server, hello())
		if err != nil {
			t.Fatalf("the turn failed: %v", err)
		}
		if got := textOf(events); got != "the whole answer" {
			t.Errorf("the reply was %q, want the whole text: a build that sends no deltas would "+
				"otherwise stream nothing at all", got)
		}
	})

	t.Run("streamed then repeated", func(t *testing.T) {
		server := &appServer{
			account: chatgpt("someone@example.com", "plus"),
			script: func(s *appServer) {
				s.say("the whole ")
				s.say("answer")
				s.notify(notifyItemCompleted, itemNotification{
					ThreadID: s.threadID, TurnID: s.turnID,
					Item: threadItem{Type: itemAgentMessage, ID: "msg-1", Text: "the whole answer"},
				})
				s.finish(turnCompleted)
			},
		}
		events, err := ask(t, server, hello())
		if err != nil {
			t.Fatalf("the turn failed: %v", err)
		}
		if got := textOf(events); got != "the whole answer" {
			t.Errorf("the reply was %q, want it once: the app server sends an item's text in "+
				"pieces and then whole, and a client that read both prints every reply twice", got)
		}
	})
}

// Thinking is thinking, and it is not the answer.
func TestReasoningArrivesAsThinkingRatherThanAsPartOfTheReply(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			s.think("weighing it up")
			s.say("the answer")
			s.finish(turnCompleted)
		},
	}
	events, err := ask(t, server, hello())
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	if got := textOf(events); got != "the answer" {
		t.Errorf("the reply was %q, want the reasoning kept out of it", got)
	}
	var thought string
	for _, event := range events {
		if event.Kind == core.EventThinking {
			thought += event.Text
		}
	}
	if thought != "weighing it up" {
		t.Errorf("the reasoning arrived as %q, want it on its own channel", thought)
	}
}

// Every way a turn can end reaches the caller as something core understands.
func TestEveryWayATurnCanEndArrivesAsADoneEventCanopyUnderstands(t *testing.T) {
	for status, want := range map[string]core.StopReason{
		turnCompleted:   core.StopEndTurn,
		turnInterrupted: core.StopCancelled,
		turnFailed:      core.StopError,
	} {
		t.Run(status, func(t *testing.T) {
			server := &appServer{
				account: chatgpt("someone@example.com", "plus"),
				script: func(s *appServer) {
					s.emitTurn(status, &turnError{Message: "it went wrong"})
				},
			}
			events, _ := ask(t, server, hello())
			if got := doneOf(t, events).StopReason; got != want {
				t.Errorf("a %q turn ended as %q, want %q", status, got, want)
			}
		})
	}
}

// A status nobody has seen is a protocol that moved, not a turn that finished.
func TestAStatusThisBuildHasNeverSeenIsAFailureRatherThanAGuess(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script:  func(s *appServer) { s.say("half an answer"); s.emitTurn("nappingBriefly", nil) },
	}
	events, err := ask(t, server, hello())
	if err == nil {
		t.Fatal("a turn that ended in a way this build cannot read was presented as complete")
	}
	if !strings.Contains(err.Error(), "nappingBriefly") {
		t.Errorf("the failure was %q, want it to quote the status so somebody can look it up", err)
	}
	if doneOf(t, events).StopReason != core.StopError {
		t.Error("the turn did not end as an error, so a caller would treat a guess as an answer")
	}
}

// A failure the app server says it will retry is not the end of the turn.
func TestAFailureTheAppServerWillRetryIsANoticeRatherThanTheEndOfTheTurn(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			s.notify(notifyError, errorNotification{
				ThreadID: s.threadID, TurnID: s.turnID,
				Error: turnError{Message: "connection reset"}, WillRetry: true,
			})
			s.say("the answer after all")
			s.finish(turnCompleted)
		},
	}
	events, err := ask(t, server, hello())
	if err != nil {
		t.Fatalf("a retryable failure ended the turn: %v", err)
	}
	if got := textOf(events); got != "the answer after all" {
		t.Errorf("the reply was %q, want the turn to have carried on", got)
	}
	if !containing(noticesOf(events), "connection reset") {
		t.Error("the retry was not mentioned, so a slow turn looks like a hang")
	}
}

// Cancelling asks rather than kills, so a partial reply survives.
func TestCancellingATurnAsksTheAgentToStopRatherThanKillingIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			s.say("as far as I got")
			cancel()
		},
	}

	events, err := askWith(t, server, ctx, hello())
	if err != nil {
		t.Fatalf("a cancelled turn was reported as a failure: %v", err)
	}
	if got := textOf(events); got != "as far as I got" {
		t.Errorf("the partial reply was %q, want it kept: a stopped turn is not a lost one", got)
	}
	if got := doneOf(t, events).StopReason; got != core.StopCancelled {
		t.Errorf("the turn ended as %q, want %q", got, core.StopCancelled)
	}
	if _, ok := server.sentMethod(methodTurnInterrupt); !ok {
		t.Error("no interrupt reached the app server, so the agent would keep working on an answer " +
			"nobody is waiting for, on somebody's plan")
	}
}

func TestCancellationStopsAnAppServerThatIgnoresTheInterrupt(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	server := &appServer{
		account:         chatgpt("someone@example.com", "plus"),
		ignoreInterrupt: true,
		script:          func(*appServer) { close(started) },
	}
	go func() {
		<-started
		cancel()
	}()

	events, err := askWith(t, server, ctx, hello())
	if err != nil {
		t.Fatalf("the forcibly stopped turn was reported as a failure: %v", err)
	}
	if got := doneOf(t, events).StopReason; got != core.StopCancelled {
		t.Fatalf("the forcibly stopped turn ended as %q", got)
	}
	if !server.wasStopped() {
		t.Error("the app server ignored turn/interrupt and Canopy left its process running")
	}
}

func TestCancellationBoundsAnAppServerThatNeverAnswersTheHandshake(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	server := &appServer{ignoreInitialize: true}

	if _, err := askWith(t, server, ctx, hello()); err == nil {
		t.Fatal("an app server that never answered initialize produced a stream")
	}
	if !server.wasStopped() {
		t.Error("the cancelled handshake left the app server process running")
	}
}

// The turn's process is a child process, and an unclosed one keeps running.
func TestClosingATurnStopsTheProcessBehindIt(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script:  func(s *appServer) { s.finish(turnCompleted) },
	}
	if _, err := ask(t, server, hello()); err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	if !server.wasStopped() {
		t.Error("the app server was left running after the turn. It holds whatever MCP servers the " +
			"user's own config.toml told it to start, so leaking one leaks several")
	}
}

// The one place this route parts company with core.Request.Validate, found on the Claude route by a
// live test and true here for the same reason.
func TestATurnThatNamesNoModelIsTheOrdinaryCaseRatherThanAMalformedOne(t *testing.T) {
	server := &appServer{
		account:     chatgpt("someone@example.com", "plus"),
		threadModel: "gpt-5.5",
		script:      func(s *appServer) { s.say("fine"); s.finish(turnCompleted) },
	}

	req := hello()
	req.Model = ""
	events, err := ask(t, server, req)
	if err != nil {
		t.Fatalf("a turn naming no model was refused: %v. A delegated credential stores none, so "+
			"this is every turn on this route", err)
	}
	if !containing(noticesOf(events), "gpt-5.5") {
		t.Error("nothing said which model answered, so the screen shows no model and the reply " +
			"came from one")
	}
}

// A model the vendor does not have is said rather than swapped silently.
func TestAModelTheDelegatedAgentDoesNotOfferIsSaidRatherThanSubstituted(t *testing.T) {
	server := &appServer{
		account:     chatgpt("someone@example.com", "plus"),
		threadModel: "gpt-5.5",
		script:      func(s *appServer) { s.finish(turnCompleted) },
	}

	req := hello()
	req.Model = "gpt-9-imaginary"
	events, err := ask(t, server, req)
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	notices := noticesOf(events)
	if !containing(notices, "gpt-9-imaginary") || !containing(notices, "gpt-5.5") {
		t.Errorf("the substitution was not reported. A screen naming one model while another "+
			"answered is the confident wrong answer this repository exists to avoid. Got: %v",
			notices)
	}
}

// A turn that does not start with the user is refused, which is the part of the contract that does
// survive here.
func TestATurnThatDoesNotStartWithTheUserIsRefused(t *testing.T) {
	server := &appServer{account: chatgpt("someone@example.com", "plus")}
	_, err := ask(t, server, core.Request{
		Messages: []core.Message{{Role: core.RoleAssistant, Text: "I will begin"}},
	})
	if err == nil {
		t.Fatal("a turn beginning with the assistant was accepted")
	}
	var providerErr *core.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != core.ErrInvalidRequest {
		t.Errorf("the refusal was %v, want a core.ProviderError of kind %s", err, core.ErrInvalidRequest)
	}
}

// The whole conversation reaches the delegated agent, with its voices labelled.
func TestTheWholeConversationReachesTheDelegatedAgentWithItsVoicesLabelled(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script:  func(s *appServer) { s.finish(turnCompleted) },
	}

	req := core.Request{
		System: "be terse",
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "what is two plus two"},
			{Role: core.RoleAssistant, Text: "four"},
			{Role: core.RoleUser, Text: "and one more"},
		},
	}
	if _, err := ask(t, server, req); err != nil {
		t.Fatalf("the turn failed: %v", err)
	}

	frame, ok := server.sentMethod(methodTurnStart)
	if !ok {
		t.Fatal("no turn reached the app server")
	}
	var params turnStartParams
	if err := json.Unmarshal(frame.Params, &params); err != nil {
		t.Fatalf("the turn frame did not decode: %v", err)
	}
	if len(params.Input) != 1 {
		t.Fatalf("the turn carried %d inputs, want one block of text", len(params.Input))
	}
	text := params.Input[0].Text
	for _, want := range []string{"what is two plus two", "Assistant: four", "and one more"} {
		if !strings.Contains(text, want) {
			t.Errorf("the transcript is missing %q. An agent handed an unlabelled wall of "+
				"alternating voices answers the wrong one", want)
		}
	}

	// The system prompt goes in as the thread's developer instructions rather than into the turn,
	// because that is the field the protocol has for it.
	opened, ok := server.sentMethod(methodThreadStart)
	if !ok {
		t.Fatal("no thread was opened")
	}
	var thread threadStartParams
	if err := json.Unmarshal(opened.Params, &thread); err != nil {
		t.Fatalf("the thread frame did not decode: %v", err)
	}
	if thread.DeveloperInstructions != "be terse" {
		t.Errorf("the system prompt reached the app server as %q, want it as the thread's "+
			"developer instructions", thread.DeveloperInstructions)
	}
}

// The two settings that decide what a delegated turn may do on somebody's machine.
func TestADelegatedThreadAsksToBeAskedAndIsRootedReadOnly(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script:  func(s *appServer) { s.finish(turnCompleted) },
	}
	if _, err := ask(t, server, hello()); err != nil {
		t.Fatalf("the turn failed: %v", err)
	}

	frame, _ := server.sentMethod(methodThreadStart)
	var params threadStartParams
	if err := json.Unmarshal(frame.Params, &params); err != nil {
		t.Fatalf("the thread frame did not decode: %v", err)
	}
	if params.ApprovalPolicy != approvalOnRequest {
		t.Errorf("the thread asked for approval policy %q, want %q. Telling the app server to stop "+
			"asking would mean the calls Canopy would have declined simply happen, with nobody told",
			params.ApprovalPolicy, approvalOnRequest)
	}
	if params.Sandbox != sandboxReadOnly {
		t.Errorf("the thread was opened with sandbox %q, want %q. Anything wider would be Canopy "+
			"granting write access to a tool loop it has no say over", params.Sandbox, sandboxReadOnly)
	}
	if !params.Ephemeral {
		t.Error("the thread was not ephemeral, so every turn writes a second copy of somebody's " +
			"conversation into ~/.codex/sessions where they did not ask for one")
	}
}

// A delegated turn is not an OpenAI API turn, and the name has to say so.
func TestTheNameOfThisRouteIsNotOpenAI(t *testing.T) {
	name := New(Installation{Binary: "codex"}).Name()
	if name == "openai" || name == "openai-compatible" {
		t.Errorf("the route calls itself %q, which would make a delegated turn indistinguishable "+
			"from a metered one everywhere a provider name is shown", name)
	}
	if name != "codex" {
		t.Errorf("the route calls itself %q, want %q", name, "codex")
	}
}

// An app server that stops mid-turn is a failure that quotes what it said.
func TestAnAppServerThatStopsMidTurnIsAFailureThatQuotesWhatItSaid(t *testing.T) {
	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script: func(s *appServer) {
			s.say("starting")
			_ = s.out.(io.Closer).Close()
		},
	}
	events, err := ask(t, server, hello())
	if err == nil {
		t.Fatal("an app server that walked out mid-turn was reported as a finished turn")
	}
	if doneOf(t, events).StopReason != core.StopError {
		t.Error("the turn did not end as an error")
	}
}

// The sign-in remedy is named when that is what the app server actually said.
func TestAnAppServerWithNobodySignedInSaysToSignInAgain(t *testing.T) {
	server := &appServer{failThread: "Unauthorized: no account is signed in"}
	_, err := ask(t, server, hello())
	if err == nil {
		t.Fatal("a turn on a signed-out Codex was accepted")
	}
	if !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("the failure was %v, want it to wrap ErrNotSignedIn so a caller can act on it", err)
	}
	if !strings.Contains(err.Error(), "signin") {
		t.Errorf("the failure was %q, want it to name the command that fixes it", err)
	}
}

// Nothing here waits forever on a request nobody answered.
func TestATurnDoesNotOutliveItsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	server := &appServer{
		account: chatgpt("someone@example.com", "plus"),
		script:  func(s *appServer) { <-ctx.Done() },
	}
	events, _ := askWith(t, server, ctx, hello())
	if got := doneOf(t, events).StopReason; got != core.StopCancelled {
		t.Errorf("a turn whose context expired ended as %q, want %q", got, core.StopCancelled)
	}
}
