package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// Plan first: approval at the task level rather than the keystroke level.
//
// Both extremes are worse than this. Per tool prompting on a fifty step task trains somebody to
// approve without reading, and by step thirty they are pressing y at a rhythm rather than at a
// decision, which is a worse outcome than not asking at all because it looks like oversight.
// Approving nothing and letting it run is the other extreme and needs no explanation.
//
// Reviewing one plan is something a person actually does properly, once, while paying attention.

// PlanPrompt is appended to the request when an agent is planning.
//
// It asks for the plan in prose rather than in a structured format on purpose. The reader is a
// person deciding whether to allow this, and a person reads prose. Parsing it back out is not
// needed: what the plan authorises is a trust level for the execution phase, not a checklist to
// verify against.
const PlanPrompt = `Before doing anything, write out what you intend to do and stop.

Say specifically:
  - which files you will read, change or create
  - which commands you will run, written out as you would run them
  - anything you are unsure about, and what you would do if it turns out differently

Do not call any tools yet. Do not start. Write the plan and stop, and wait to be told to go ahead.`

// PlanOutcome is a plan awaiting a decision.
type PlanOutcome struct {
	// Plan is what the agent said it intends to do, verbatim.
	Plan string
	// Usage is what producing the plan cost, which is real and belongs in the session total.
	Usage core.Usage
}

// Plan asks the agent what it intends to do, without letting it do anything.
//
// The tools are still described to the model, because a plan written by something that does not know
// what it can do is a plan that proposes the impossible. What is withheld is permission to call
// them, which is enforced rather than requested: the loop runs with an approver that refuses
// everything and a trust level of read-only, so a model that ignores the instruction and calls a
// tool anyway is stopped by the permission layer rather than by its own good behaviour.
func (l *Loop) Plan(ctx context.Context, req core.Request, obs Observer) (PlanOutcome, error) {
	planning := *l
	planning.Trust = core.TrustReadOnly
	planning.Approver = DenyAll
	// A fresh grant set, so an approval given earlier in the session cannot let a tool run during
	// planning. Reusing the session's grants would mean "always allow edits" quietly turning plan
	// mode into ordinary mode.
	planning.Grants = permission.NewGrants()
	// One step. A plan is one answer, and a model that wants a second step during planning is one
	// that has started working.
	planning.MaxSteps = 1

	req.Messages = append(append([]core.Message(nil), req.Messages...),
		core.Message{Role: core.RoleUser, Text: PlanPrompt})

	outcome, err := planning.Run(ctx, req, obs)
	if err != nil {
		return PlanOutcome{}, err
	}

	plan := lastAssistantText(outcome.Messages)
	if strings.TrimSpace(plan) == "" {
		return PlanOutcome{}, fmt.Errorf(
			"the agent produced no plan, so there is nothing to approve")
	}
	return PlanOutcome{Plan: plan, Usage: outcome.Usage}, nil
}

// Execute carries out an approved plan.
//
// The plan is put back into the conversation as what the agent said, and the approval as what the
// user replied. That ordering matters: a model that reads its own plan as its own words follows it,
// where a model handed the same text as an instruction from somebody else argues with it.
func (l *Loop) Execute(
	ctx context.Context, req core.Request, plan string, obs Observer,
) (Outcome, error) {
	req.Messages = append(append([]core.Message(nil), req.Messages...),
		core.Message{Role: core.RoleAssistant, Text: plan},
		core.Message{Role: core.RoleUser, Text: ExecutePrompt})

	return l.Run(ctx, req, obs)
}

// ExecutePrompt is what the user's approval looks like to the model.
//
// The second sentence is the load bearing one. An approval covers the plan that was read, and an
// agent that discovers halfway through that the real fix is somewhere else has been authorised for
// something nobody agreed to. Telling it to stop and say so is cheaper than any mechanism that tries
// to detect the departure afterwards.
const ExecutePrompt = `That plan is approved. Go ahead and carry it out.

If you find that the plan will not work, or that the real fix is somewhere you did not mention, ` +
	`stop and say so rather than carrying on. What was approved was the plan above, not whatever ` +
	`turns out to be necessary.`

// lastAssistantText finds what the model actually said.
func lastAssistantText(messages []core.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == core.RoleAssistant && messages[i].Text != "" {
			return messages[i].Text
		}
	}
	return ""
}
