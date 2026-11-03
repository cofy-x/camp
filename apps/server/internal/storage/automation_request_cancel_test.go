package storage

import (
	"sync"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestAutomationRequestCancellationFencesEveryDelayedWrite(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	for _, id := range []string{"policy-never-sent", "credential-never-sent", "subscription-never-sent", "delivery-never-sent"} {
		cancelled, err := s.CancelAutomationRequest(actor, id)
		if err != nil || cancelled.Kind != "cancelled" {
			t.Fatal(cancelled, err)
		}
		replay, err := s.CancelAutomationRequest(actor, id)
		if err != nil || replay.Kind != "cancelled" {
			t.Fatal(replay, err)
		}
	}
	_, err := s.SaveAutomationPolicy(actor, room.ID, "room", orchestration.PolicyInput{RequestID: "policy-never-sent", Mode: "observe"})
	requireCode(t, err, "request_cancelled")
	_, _, err = s.FindAutomationCredential(actor, "any-source", AutomationCredentialInput{RequestID: "credential-never-sent", Token: "private", ActorID: 7})
	requireCode(t, err, "request_cancelled")
	_, err = s.SetTaskSubscription(actor, task.ID, orchestration.SubscriptionInput{RequestID: "subscription-never-sent", SourceID: "room", ThreadKey: room.ID + ":room", Enabled: true})
	requireCode(t, err, "request_cancelled")
	_, err = s.OperateAutomationDelivery(actor, "any-delivery", "resend", AutomationDeliveryInput{RequestID: "delivery-never-sent"})
	requireCode(t, err, "request_cancelled")
	assertScalar(t, s, "SELECT count(*) FROM automation_policies", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_subscriptions", 0)
}

func TestAutomationRequestCancellationReturnsExistingReceiptAndIsActorScoped(t *testing.T) {
	s, actor, member, room, _, _ := taskFixture(t)
	input := orchestration.PolicyInput{RequestID: "same-user-request", Mode: "observe"}
	policy, err := s.SaveAutomationPolicy(actor, room.ID, "room", input)
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.CancelAutomationRequest(actor, input.RequestID)
	if err != nil || found.Kind != "policy" || found.Policy == nil || found.Policy.ID != policy.ID {
		t.Fatal("cancellation erased an accepted result", found, err)
	}
	other, err := s.CancelAutomationRequest(member, input.RequestID)
	if err != nil || other.Kind != "cancelled" {
		t.Fatal(other, err)
	}
	replayed, err := s.SaveAutomationPolicy(actor, room.ID, "room", input)
	if err != nil || replayed.ID != policy.ID {
		t.Fatal("another actor's tombstone fenced the original user", replayed, err)
	}
	view, err := s.AutomationRequest(member, input.RequestID)
	if err != nil || view.Kind != "cancelled" {
		t.Fatal(view, err)
	}
}

func TestAutomationRequestCancellationRacesOriginalCommitAtomically(t *testing.T) {
	for range 5 {
		s, actor, _, room, _, _ := taskFixture(t)
		input := orchestration.PolicyInput{RequestID: "racing-request", Mode: "observe"}
		var mutationErr, cancelErr error
		var cancelled AutomationReceipt
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, mutationErr = s.SaveAutomationPolicy(actor, room.ID, "room", input) }()
		go func() { defer wg.Done(); cancelled, cancelErr = s.CancelAutomationRequest(actor, input.RequestID) }()
		wg.Wait()
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
		view, err := s.AutomationRequest(actor, input.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Kind == "cancelled" {
			requireCode(t, mutationErr, "request_cancelled")
			assertScalar(t, s, "SELECT count(*) FROM automation_policies", 0)
		} else if view.Kind == "policy" {
			if mutationErr != nil || cancelled.Kind != "policy" {
				t.Fatal(mutationErr, cancelled)
			}
			assertScalar(t, s, "SELECT count(*) FROM automation_policies", 1)
		} else {
			t.Fatal("race did not produce one durable outcome", view)
		}
		assertScalar(t, s, "SELECT count(*) FROM automation_requests", 1)
	}
}
