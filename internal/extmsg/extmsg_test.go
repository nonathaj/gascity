package extmsg

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestBindingServiceBindEnforcesOwnershipAndConflict(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	fabric := NewServices(store)
	svc := fabric.Bindings
	ref := testConversationRef()

	first, err := svc.Bind(context.Background(), testAdapterCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow(),
		Metadata:     map[string]string{"source": "discord"},
	})
	if err != nil {
		t.Fatalf("Bind(first): %v", err)
	}
	if first.BindingGeneration != 1 {
		t.Fatalf("BindingGeneration = %d, want 1", first.BindingGeneration)
	}

	second, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Bind(idempotent): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent bind changed ID: got %s want %s", second.ID, first.ID)
	}
	if second.BindingGeneration != first.BindingGeneration {
		t.Fatalf("idempotent bind changed generation: got %d want %d", second.BindingGeneration, first.BindingGeneration)
	}

	_, err = svc.Bind(context.Background(), Caller{
		Kind:      CallerAdapter,
		ID:        "adapter-2",
		Provider:  "slack",
		AccountID: "acct-1",
	}, BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow(),
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Bind(foreign adapter) error = %v, want ErrUnauthorized", err)
	}

	_, err = svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-b",
		Now:          testNow(),
	})
	if !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("Bind(conflict) error = %v, want ErrBindingConflict", err)
	}

	got, err := svc.ResolveByConversation(context.Background(), ref)
	if err != nil {
		t.Fatalf("ResolveByConversation: %v", err)
	}
	if got == nil || got.SessionID != "sess-a" {
		t.Fatalf("ResolveByConversation session = %#v, want sess-a", got)
	}
}

func TestBindingServiceExpiredBindingIsMissAndRebinds(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	svc := NewServices(store).Bindings
	ref := testConversationRef()
	expiredAt := testNow().Add(-time.Minute)

	first, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		ExpiresAt:    &expiredAt,
		Now:          testNow().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Bind(expired): %v", err)
	}
	if first.BindingGeneration != 1 {
		t.Fatalf("expired BindingGeneration = %d, want 1", first.BindingGeneration)
	}

	got, err := svc.ResolveByConversation(context.Background(), ref)
	if err != nil {
		t.Fatalf("ResolveByConversation(expired): %v", err)
	}
	if got != nil {
		t.Fatalf("ResolveByConversation(expired) = %#v, want nil", got)
	}

	second, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-b",
		Now:          testNow(),
	})
	if err != nil {
		t.Fatalf("Bind(rebind): %v", err)
	}
	if second.BindingGeneration != 2 {
		t.Fatalf("rebind BindingGeneration = %d, want 2", second.BindingGeneration)
	}
}

func TestBindingServiceBindSeparatesConversationVariants(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	svc := NewServices(store).Bindings

	threadRef := testConversationRef()
	roomRef := testConversationRef()
	roomRef.Kind = ConversationRoom
	roomRef.ParentConversationID = "parent-1"

	if bindingConversationLabel(threadRef) == bindingConversationLabel(roomRef) {
		t.Fatal("bindingConversationLabel should differ for distinct ConversationRef variants")
	}

	if _, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: threadRef,
		SessionID:    "sess-thread",
		Now:          testNow(),
	}); err != nil {
		t.Fatalf("Bind(thread): %v", err)
	}
	if _, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: roomRef,
		SessionID:    "sess-room",
		Now:          testNow(),
	}); err != nil {
		t.Fatalf("Bind(room): %v", err)
	}

	threadBinding, err := svc.ResolveByConversation(context.Background(), threadRef)
	if err != nil {
		t.Fatalf("ResolveByConversation(thread): %v", err)
	}
	roomBinding, err := svc.ResolveByConversation(context.Background(), roomRef)
	if err != nil {
		t.Fatalf("ResolveByConversation(room): %v", err)
	}
	if threadBinding == nil || threadBinding.SessionID != "sess-thread" {
		t.Fatalf("thread binding = %#v, want sess-thread", threadBinding)
	}
	if roomBinding == nil || roomBinding.SessionID != "sess-room" {
		t.Fatalf("room binding = %#v, want sess-room", roomBinding)
	}
}

func TestBindingServiceConcurrentBindConflicts(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	fabric := NewServices(store)
	ref := testConversationRef()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, sessionID := range []string{"sess-a", "sess-b"} {
		wg.Add(1)
		go func(sessionID string) {
			defer wg.Done()
			_, err := fabric.Bindings.Bind(context.Background(), testControllerCaller(), BindInput{
				Conversation: ref,
				SessionID:    sessionID,
				Now:          testNow(),
			})
			errs <- err
		}(sessionID)
	}
	wg.Wait()
	close(errs)

	var successes int
	var conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrBindingConflict):
			conflicts++
		default:
			t.Fatalf("Bind(concurrent) unexpected error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent bind results = successes:%d conflicts:%d, want 1/1", successes, conflicts)
	}
}

func TestBindingServiceConcurrentBindConflictsAcrossBundles(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	fabricA := NewServices(store)
	fabricB := NewServices(store)
	ref := testConversationRef()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, svc := range []BindingService{fabricA.Bindings, fabricB.Bindings} {
		wg.Add(1)
		go func(i int, svc BindingService) {
			defer wg.Done()
			_, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
				Conversation: ref,
				SessionID:    "sess-" + strconv.Itoa(i),
				Now:          testNow(),
			})
			errs <- err
		}(i, svc)
	}
	wg.Wait()
	close(errs)

	var successes int
	var conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrBindingConflict):
			conflicts++
		default:
			t.Fatalf("Bind(across bundles) unexpected error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("across-bundle bind results = successes:%d conflicts:%d, want 1/1", successes, conflicts)
	}
}

func TestBindingServiceUnbindClearsDeliveryContext(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	fabric := NewServices(store)
	delivery := fabric.Delivery
	svc := fabric.Bindings
	ref := testConversationRef()

	binding, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow(),
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := delivery.Record(context.Background(), testControllerCaller(), DeliveryContextRecord{
		SessionID:         "sess-a",
		Conversation:      ref,
		BindingGeneration: binding.BindingGeneration,
		LastPublishedAt:   testNow(),
		LastMessageID:     "msg-1",
		SourceSessionID:   "sess-a",
		Metadata:          map[string]string{"route": "thread-reply"},
	}); err != nil {
		t.Fatalf("Record(delivery): %v", err)
	}

	got, err := delivery.Resolve(context.Background(), "sess-a", ref)
	if err != nil {
		t.Fatalf("Resolve(delivery before unbind): %v", err)
	}
	if got == nil {
		t.Fatal("Resolve(delivery before unbind) = nil, want record")
	}
	if got.SessionID != "sess-a" || got.BindingGeneration != binding.BindingGeneration {
		t.Fatalf("Resolve(delivery before unbind) identity = %#v, want sess-a generation %d", got, binding.BindingGeneration)
	}
	if !got.LastPublishedAt.Equal(testNow()) || got.LastMessageID != "msg-1" || got.SourceSessionID != "sess-a" {
		t.Fatalf("Resolve(delivery before unbind) fields = %#v, want published/msg/source values", got)
	}
	if got.Metadata["route"] != "thread-reply" {
		t.Fatalf("Resolve(delivery before unbind) metadata = %#v, want route=thread-reply", got.Metadata)
	}

	unbound, err := svc.Unbind(context.Background(), testControllerCaller(), UnbindInput{
		Conversation: &ref,
		Now:          testNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	if len(unbound) != 1 || unbound[0].Status != BindingEnded {
		t.Fatalf("Unbind() = %#v, want one ended binding", unbound)
	}

	got, err = delivery.Resolve(context.Background(), "sess-a", ref)
	if err != nil {
		t.Fatalf("Resolve(delivery after unbind): %v", err)
	}
	if got != nil {
		t.Fatalf("Resolve(delivery after unbind) = %#v, want nil", got)
	}

	items, err := store.ListByLabel(deliveryRouteLabel(ref, "sess-a"), 0)
	if err != nil {
		t.Fatalf("ListByLabel(delivery route): %v", err)
	}
	if len(items) != 1 || items[0].Status != "closed" {
		t.Fatalf("delivery bead status = %#v, want one closed bead", items)
	}
}

func TestDeliveryContextResolveKeepsValidRouteWhileClosingStaleRoute(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	fabric := NewServices(store)
	delivery := fabric.Delivery
	svc := fabric.Bindings
	ref := testConversationRef()

	firstBinding, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow(),
	})
	if err != nil {
		t.Fatalf("Bind(first): %v", err)
	}
	if _, err := svc.Unbind(context.Background(), testControllerCaller(), UnbindInput{
		Conversation: &ref,
		Now:          testNow().Add(time.Minute),
	}); err != nil {
		t.Fatalf("Unbind(first): %v", err)
	}
	secondBinding, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow().Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Bind(second): %v", err)
	}
	if secondBinding.BindingGeneration != firstBinding.BindingGeneration+1 {
		t.Fatalf("BindingGeneration(second) = %d, want %d", secondBinding.BindingGeneration, firstBinding.BindingGeneration+1)
	}
	if err := delivery.Record(context.Background(), testControllerCaller(), DeliveryContextRecord{
		SessionID:         "sess-a",
		Conversation:      ref,
		BindingGeneration: secondBinding.BindingGeneration,
		LastPublishedAt:   testNow().Add(2 * time.Minute),
		LastMessageID:     "msg-2",
	}); err != nil {
		t.Fatalf("Record(valid delivery): %v", err)
	}
	if _, err := store.Create(beads.Bead{
		Title:  "stale delivery",
		Type:   "external_delivery",
		Labels: []string{labelDeliveryBase, deliveryRouteLabel(ref, "sess-a"), deliverySessionLabel("sess-a")},
		Metadata: encodeMetadataFields(nil, map[string]string{
			"schema_version":         strconv.Itoa(schemaVersion),
			"session_id":             "sess-a",
			"scope_id":               ref.ScopeID,
			"provider":               ref.Provider,
			"account_id":             ref.AccountID,
			"conversation_id":        ref.ConversationID,
			"parent_conversation_id": ref.ParentConversationID,
			"conversation_kind":      string(ref.Kind),
			"binding_generation":     strconv.FormatInt(firstBinding.BindingGeneration, 10),
			"last_published_at":      formatTime(testNow()),
			"last_message_id":        "msg-stale",
		}),
	}); err != nil {
		t.Fatalf("Create(stale delivery): %v", err)
	}

	got, err := delivery.Resolve(context.Background(), "sess-a", ref)
	if err != nil {
		t.Fatalf("Resolve(delivery): %v", err)
	}
	if got == nil || got.BindingGeneration != secondBinding.BindingGeneration || got.LastMessageID != "msg-2" {
		t.Fatalf("Resolve(delivery) = %#v, want valid generation %d msg-2", got, secondBinding.BindingGeneration)
	}

	items, err := store.ListByLabel(deliveryRouteLabel(ref, "sess-a"), 0)
	if err != nil {
		t.Fatalf("ListByLabel(delivery route): %v", err)
	}
	var openCount int
	var closedCount int
	for _, item := range items {
		switch item.Status {
		case "open":
			openCount++
		case "closed":
			closedCount++
		}
	}
	if openCount != 1 || closedCount != 1 {
		t.Fatalf("delivery route statuses = %#v, want one open and one closed", items)
	}
}

func TestBindingServiceUnbindBySessionReturnsPartialClosedOnFailure(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	refFirst := testConversationRef()
	refFirst.ConversationID = "thread-a"
	refSecond := testConversationRef()
	refSecond.ConversationID = "thread-b"
	if conversationLockKey(refFirst) > conversationLockKey(refSecond) {
		refFirst, refSecond = refSecond, refFirst
	}
	delivery := &selectiveFailingDeliveryContextService{
		failConversationIDs: map[string]bool{refFirst.ConversationID: true},
		err:                 errors.New("boom"),
	}
	svc := newBindingService(store, delivery, newBindingLockPool())

	bindingFirst, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: refFirst,
		SessionID:    "sess-a",
		Now:          testNow(),
	})
	if err != nil {
		t.Fatalf("Bind(first): %v", err)
	}
	bindingSecond, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: refSecond,
		SessionID:    "sess-a",
		Now:          testNow(),
	})
	if err != nil {
		t.Fatalf("Bind(second): %v", err)
	}

	closed, err := svc.Unbind(context.Background(), testControllerCaller(), UnbindInput{
		SessionID: "sess-a",
		Now:       testNow().Add(time.Minute),
	})
	if err == nil {
		t.Fatal("Unbind(session-wide) error = nil, want delivery clear failure")
	}
	if len(closed) != 1 || closed[0].ID != bindingFirst.ID || closed[0].Status != BindingEnded {
		t.Fatalf("Unbind(session-wide) partial closed = %#v, want only first binding ended", closed)
	}

	itemFirst, err := store.Get(bindingFirst.ID)
	if err != nil {
		t.Fatalf("Get(bindingFirst): %v", err)
	}
	if itemFirst.Status != "closed" {
		t.Fatalf("bindingFirst status = %q, want closed", itemFirst.Status)
	}
	itemSecond, err := store.Get(bindingSecond.ID)
	if err != nil {
		t.Fatalf("Get(bindingSecond): %v", err)
	}
	if itemSecond.Status != "open" {
		t.Fatalf("bindingSecond status = %q, want open", itemSecond.Status)
	}
}

func TestBindingServiceUnbindKeepsClosedBindingWhenDeliveryClearFails(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	delivery := &failingDeliveryContextService{err: errors.New("boom")}
	svc := newBindingService(store, delivery, newBindingLockPool())
	ref := testConversationRef()

	binding, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow(),
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	closed, err := svc.Unbind(context.Background(), testControllerCaller(), UnbindInput{
		Conversation: &ref,
		Now:          testNow().Add(time.Minute),
	})
	if err == nil {
		t.Fatal("Unbind() error = nil, want delivery clear failure")
	}
	if len(closed) != 1 || closed[0].ID != binding.ID || closed[0].Status != BindingEnded {
		t.Fatalf("Unbind() closed = %#v, want one ended binding", closed)
	}

	item, err := store.Get(binding.ID)
	if err != nil {
		t.Fatalf("Get(binding): %v", err)
	}
	if item.Status != "closed" {
		t.Fatalf("binding status after failed clear = %q, want closed", item.Status)
	}
}

func TestBindingServiceListBySessionReturnsOnlyBindings(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	fabric := NewServices(store)
	delivery := fabric.Delivery
	bindings := fabric.Bindings
	groups := fabric.Groups
	ref := testConversationRef()

	binding, err := bindings.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow(),
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := delivery.Record(context.Background(), testControllerCaller(), DeliveryContextRecord{
		SessionID:         "sess-a",
		Conversation:      ref,
		BindingGeneration: binding.BindingGeneration,
		LastPublishedAt:   testNow(),
	}); err != nil {
		t.Fatalf("Record(delivery): %v", err)
	}
	group, err := groups.EnsureGroup(context.Background(), testControllerCaller(), EnsureGroupInput{
		RootConversation: ref,
		Mode:             GroupModeLauncher,
	})
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	if _, err := groups.UpsertParticipant(context.Background(), testControllerCaller(), UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "alpha",
		SessionID: "sess-a",
	}); err != nil {
		t.Fatalf("UpsertParticipant: %v", err)
	}

	got, err := bindings.ListBySession(context.Background(), "sess-a")
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListBySession len = %d, want 1", len(got))
	}
	if got[0].ID != binding.ID {
		t.Fatalf("ListBySession binding ID = %s, want %s", got[0].ID, binding.ID)
	}
}

func TestBindingServiceTouchDebouncesMetadataWrites(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	svc := newBindingService(store, nil, newBindingLockPool(), WithBindingTouchDebounce(time.Hour))
	ref := testConversationRef()
	start := testNow()

	binding, err := svc.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          start,
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if err := svc.Touch(context.Background(), testControllerCaller(), binding.ID, start.Add(10*time.Minute)); err != nil {
		t.Fatalf("Touch(debounced): %v", err)
	}
	item, err := store.Get(binding.ID)
	if err != nil {
		t.Fatalf("Get(binding): %v", err)
	}
	lastTouched, err := parseTime(item.Metadata, "last_touched_at")
	if err != nil {
		t.Fatalf("parseTime(last_touched_at): %v", err)
	}
	if !lastTouched.Equal(start) {
		t.Fatalf("last_touched_at after debounced touch = %s, want %s", lastTouched, start)
	}

	if err := svc.Touch(context.Background(), testControllerCaller(), binding.ID, start.Add(2*time.Hour)); err != nil {
		t.Fatalf("Touch(applied): %v", err)
	}
	item, err = store.Get(binding.ID)
	if err != nil {
		t.Fatalf("Get(binding after applied touch): %v", err)
	}
	lastTouched, err = parseTime(item.Metadata, "last_touched_at")
	if err != nil {
		t.Fatalf("parseTime(last_touched_at after apply): %v", err)
	}
	want := start.Add(2 * time.Hour)
	if !lastTouched.Equal(want) {
		t.Fatalf("last_touched_at after applied touch = %s, want %s", lastTouched, want)
	}
}

func TestDeliveryContextRecordRejectsBindingMismatch(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	fabric := NewServices(store)
	delivery := fabric.Delivery
	bindings := fabric.Bindings
	ref := testConversationRef()

	if _, err := bindings.Bind(context.Background(), testControllerCaller(), BindInput{
		Conversation: ref,
		SessionID:    "sess-a",
		Now:          testNow(),
	}); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	err := delivery.Record(context.Background(), testControllerCaller(), DeliveryContextRecord{
		SessionID:         "sess-a",
		Conversation:      ref,
		BindingGeneration: 2,
		LastPublishedAt:   testNow(),
	})
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("Record(binding mismatch) error = %v, want ErrBindingMismatch", err)
	}
}

func TestGroupServiceRoutesExplicitAndImplicitTargets(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	svc := NewGroupService(store)
	ref := testConversationRef()

	group, err := svc.EnsureGroup(context.Background(), testControllerCaller(), EnsureGroupInput{
		RootConversation:   ref,
		Mode:               GroupModeLauncher,
		DefaultHandle:      "alpha",
		AmbientReadEnabled: true,
	})
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}

	if _, err := svc.UpsertParticipant(context.Background(), testControllerCaller(), UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "alpha",
		SessionID: "sess-a",
		Public:    true,
	}); err != nil {
		t.Fatalf("UpsertParticipant(alpha): %v", err)
	}
	if _, err := svc.UpsertParticipant(context.Background(), testControllerCaller(), UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "beta",
		SessionID: "sess-b",
	}); err != nil {
		t.Fatalf("UpsertParticipant(beta): %v", err)
	}
	if _, err := svc.UpsertParticipant(context.Background(), testControllerCaller(), UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "gamma",
		SessionID: "sess-c",
	}); err != nil {
		t.Fatalf("UpsertParticipant(gamma): %v", err)
	}

	defaultDecision, err := svc.ResolveInbound(context.Background(), ExternalInboundMessage{
		Conversation: ref,
	})
	if err != nil {
		t.Fatalf("ResolveInbound(default): %v", err)
	}
	if defaultDecision.Match != GroupRouteDefault || defaultDecision.TargetSessionID != "sess-a" {
		t.Fatalf("ResolveInbound(default) = %#v, want default->sess-a", defaultDecision)
	}

	explicitDecision, err := svc.ResolveInbound(context.Background(), ExternalInboundMessage{
		Conversation:   ref,
		ExplicitTarget: "beta",
	})
	if err != nil {
		t.Fatalf("ResolveInbound(explicit): %v", err)
	}
	if explicitDecision.Match != GroupRouteExplicitTarget || explicitDecision.TargetSessionID != "sess-b" || !explicitDecision.UpdateCursor {
		t.Fatalf("ResolveInbound(explicit) = %#v, want explicit->sess-b with cursor update", explicitDecision)
	}
	if !sameMembers(explicitDecision.PassiveSessionIDs, []string{"sess-a", "sess-c"}) {
		t.Fatalf("ResolveInbound(explicit) passive = %#v, want sess-a+sesso-c", explicitDecision.PassiveSessionIDs)
	}

	if err := svc.UpdateCursor(context.Background(), testControllerCaller(), UpdateCursorInput{
		RootConversation: ref,
		Handle:           "beta",
	}); err != nil {
		t.Fatalf("UpdateCursor(beta): %v", err)
	}

	implicitDecision, err := svc.ResolveInbound(context.Background(), ExternalInboundMessage{
		Conversation: ref,
	})
	if err != nil {
		t.Fatalf("ResolveInbound(last addressed): %v", err)
	}
	if implicitDecision.Match != GroupRouteLastAddressed || implicitDecision.TargetSessionID != "sess-b" {
		t.Fatalf("ResolveInbound(last addressed) = %#v, want last_addressed->sess-b", implicitDecision)
	}

	if err := svc.UpdateCursor(context.Background(), testControllerCaller(), UpdateCursorInput{
		RootConversation: ref,
		Handle:           "",
	}); err != nil {
		t.Fatalf("UpdateCursor(clear): %v", err)
	}
	clearedDecision, err := svc.ResolveInbound(context.Background(), ExternalInboundMessage{
		Conversation: ref,
	})
	if err != nil {
		t.Fatalf("ResolveInbound(cleared cursor): %v", err)
	}
	if clearedDecision.Match != GroupRouteDefault || clearedDecision.TargetSessionID != "sess-a" {
		t.Fatalf("ResolveInbound(cleared cursor) = %#v, want default->sess-a", clearedDecision)
	}
}

func TestGroupServiceEnsureGroupPreservesLastAddressedHandle(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	svc := NewGroupService(store)
	ref := testConversationRef()

	group, err := svc.EnsureGroup(context.Background(), testControllerCaller(), EnsureGroupInput{
		RootConversation: ref,
		Mode:             GroupModeLauncher,
		DefaultHandle:    "alpha",
	})
	if err != nil {
		t.Fatalf("EnsureGroup(create): %v", err)
	}
	if _, err := svc.UpsertParticipant(context.Background(), testControllerCaller(), UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "alpha",
		SessionID: "sess-a",
	}); err != nil {
		t.Fatalf("UpsertParticipant(alpha): %v", err)
	}
	if _, err := svc.UpsertParticipant(context.Background(), testControllerCaller(), UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "beta",
		SessionID: "sess-b",
	}); err != nil {
		t.Fatalf("UpsertParticipant(beta): %v", err)
	}
	if err := svc.UpdateCursor(context.Background(), testControllerCaller(), UpdateCursorInput{
		RootConversation: ref,
		Handle:           "beta",
	}); err != nil {
		t.Fatalf("UpdateCursor(beta): %v", err)
	}

	updated, err := svc.EnsureGroup(context.Background(), testControllerCaller(), EnsureGroupInput{
		RootConversation: ref,
		Mode:             GroupModeLauncher,
		DefaultHandle:    "alpha",
	})
	if err != nil {
		t.Fatalf("EnsureGroup(update): %v", err)
	}
	if updated.LastAddressedHandle != "beta" {
		t.Fatalf("EnsureGroup(update) last addressed = %q, want beta", updated.LastAddressedHandle)
	}
}

func TestBindingServiceResolveByConversationRejectsDuplicateActiveBindings(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	fabric := NewServices(store)
	ref := testConversationRef()

	if _, err := store.Create(beads.Bead{
		Title:  conversationTitle(ref),
		Type:   "external_binding",
		Labels: []string{labelBindingBase, bindingConversationLabel(ref), bindingSessionLabel("sess-a")},
		Metadata: encodeMetadataFields(nil, map[string]string{
			"schema_version":         strconv.Itoa(schemaVersion),
			"scope_id":               ref.ScopeID,
			"provider":               ref.Provider,
			"account_id":             ref.AccountID,
			"conversation_id":        ref.ConversationID,
			"parent_conversation_id": ref.ParentConversationID,
			"conversation_kind":      string(ref.Kind),
			"session_id":             "sess-a",
			"binding_generation":     "1",
			"bound_at":               formatTime(testNow()),
			"last_touched_at":        formatTime(testNow()),
			"created_by_kind":        string(CallerController),
			"created_by_id":          "controller-1",
		}),
	}); err != nil {
		t.Fatalf("Create(binding a): %v", err)
	}
	if _, err := store.Create(beads.Bead{
		Title:  conversationTitle(ref),
		Type:   "external_binding",
		Labels: []string{labelBindingBase, bindingConversationLabel(ref), bindingSessionLabel("sess-b")},
		Metadata: encodeMetadataFields(nil, map[string]string{
			"schema_version":         strconv.Itoa(schemaVersion),
			"scope_id":               ref.ScopeID,
			"provider":               ref.Provider,
			"account_id":             ref.AccountID,
			"conversation_id":        ref.ConversationID,
			"parent_conversation_id": ref.ParentConversationID,
			"conversation_kind":      string(ref.Kind),
			"session_id":             "sess-b",
			"binding_generation":     "2",
			"bound_at":               formatTime(testNow().Add(time.Minute)),
			"last_touched_at":        formatTime(testNow().Add(time.Minute)),
			"created_by_kind":        string(CallerController),
			"created_by_id":          "controller-1",
		}),
	}); err != nil {
		t.Fatalf("Create(binding b): %v", err)
	}

	_, err := fabric.Bindings.ResolveByConversation(context.Background(), ref)
	if !errors.Is(err, ErrInvariantViolation) {
		t.Fatalf("ResolveByConversation(duplicate active) error = %v, want ErrInvariantViolation", err)
	}
}

func TestGroupServiceResolveInboundRejectsDuplicateParticipants(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	svc := NewGroupService(store)
	ref := testConversationRef()

	group, err := svc.EnsureGroup(context.Background(), testControllerCaller(), EnsureGroupInput{
		RootConversation: ref,
		Mode:             GroupModeLauncher,
		DefaultHandle:    "alpha",
	})
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	if _, err := store.Create(beads.Bead{
		Title:  group.ID + "/alpha-a",
		Type:   "external_group_participant",
		Labels: []string{labelGroupParticipantBase, groupParticipantLabel(group.ID), groupParticipantSessionLabel("sess-a")},
		Metadata: encodeMetadataFields(nil, map[string]string{
			"schema_version": strconv.Itoa(schemaVersion),
			"group_id":       group.ID,
			"handle":         "alpha",
			"session_id":     "sess-a",
			"public":         "false",
		}),
	}); err != nil {
		t.Fatalf("Create(participant a): %v", err)
	}
	if _, err := store.Create(beads.Bead{
		Title:  group.ID + "/alpha-b",
		Type:   "external_group_participant",
		Labels: []string{labelGroupParticipantBase, groupParticipantLabel(group.ID), groupParticipantSessionLabel("sess-b")},
		Metadata: encodeMetadataFields(nil, map[string]string{
			"schema_version": strconv.Itoa(schemaVersion),
			"group_id":       group.ID,
			"handle":         "alpha",
			"session_id":     "sess-b",
			"public":         "false",
		}),
	}); err != nil {
		t.Fatalf("Create(participant b): %v", err)
	}

	_, err = svc.ResolveInbound(context.Background(), ExternalInboundMessage{Conversation: ref})
	if !errors.Is(err, ErrInvariantViolation) {
		t.Fatalf("ResolveInbound(duplicate participants) error = %v, want ErrInvariantViolation", err)
	}
}

func TestGroupServiceUpsertParticipantPreservesSessionLabelOnIdempotentUpdate(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	svc := NewGroupService(store)
	ref := testConversationRef()

	group, err := svc.EnsureGroup(context.Background(), testControllerCaller(), EnsureGroupInput{
		RootConversation: ref,
		Mode:             GroupModeLauncher,
	})
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}

	participant, err := svc.UpsertParticipant(context.Background(), testControllerCaller(), UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "alpha",
		SessionID: "sess-a",
	})
	if err != nil {
		t.Fatalf("UpsertParticipant(create): %v", err)
	}
	if _, err := svc.UpsertParticipant(context.Background(), testControllerCaller(), UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "alpha",
		SessionID: "sess-a",
		Metadata:  map[string]string{"note": "updated"},
	}); err != nil {
		t.Fatalf("UpsertParticipant(idempotent): %v", err)
	}

	items, err := store.ListByLabel(groupParticipantSessionLabel("sess-a"), 0)
	if err != nil {
		t.Fatalf("ListByLabel(groupParticipantSessionLabel): %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("participant session label count = %d, want 1", len(items))
	}
	if items[0].ID != participant.ID {
		t.Fatalf("participant session label ID = %s, want %s", items[0].ID, participant.ID)
	}
}

func TestGroupServiceParticipantMutationsEnforceOwnership(t *testing.T) {
	freezeTestClock(t)
	store := beads.NewMemStore()
	svc := NewGroupService(store)
	ref := testConversationRef()

	group, err := svc.EnsureGroup(context.Background(), testControllerCaller(), EnsureGroupInput{
		RootConversation: ref,
		Mode:             GroupModeLauncher,
	})
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}

	_, err = svc.UpsertParticipant(context.Background(), Caller{
		Kind:      CallerAdapter,
		ID:        "adapter-2",
		Provider:  "slack",
		AccountID: "acct-1",
	}, UpsertParticipantInput{
		GroupID:   group.ID,
		Handle:    "alpha",
		SessionID: "sess-a",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("UpsertParticipant(foreign adapter) error = %v, want ErrUnauthorized", err)
	}
}

func testConversationRef() ConversationRef {
	return ConversationRef{
		ScopeID:        "city-1",
		Provider:       "discord",
		AccountID:      "acct-1",
		ConversationID: "thread-1",
		Kind:           ConversationThread,
	}
}

func testControllerCaller() Caller {
	return Caller{Kind: CallerController, ID: "controller-1"}
}

func testAdapterCaller() Caller {
	return Caller{
		Kind:      CallerAdapter,
		ID:        "adapter-1",
		Provider:  "discord",
		AccountID: "acct-1",
	}
}

func testNow() time.Time {
	return time.Date(2026, time.March, 23, 9, 0, 0, 0, time.UTC)
}

func freezeTestClock(t *testing.T) {
	t.Helper()
	prev := timeNow
	timeNow = func() time.Time { return testNow() }
	t.Cleanup(func() {
		timeNow = prev
	})
}

func sameMembers(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	got = slices.Clone(got)
	want = slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}

type failingDeliveryContextService struct {
	err error
}

func (f *failingDeliveryContextService) Record(context.Context, Caller, DeliveryContextRecord) error {
	return nil
}

func (f *failingDeliveryContextService) Resolve(context.Context, string, ConversationRef) (*DeliveryContextRecord, error) {
	return nil, nil
}

func (f *failingDeliveryContextService) ClearForConversation(context.Context, string, ConversationRef) error {
	return f.err
}

type selectiveFailingDeliveryContextService struct {
	failConversationIDs map[string]bool
	err                 error
}

func (f *selectiveFailingDeliveryContextService) Record(context.Context, Caller, DeliveryContextRecord) error {
	return nil
}

func (f *selectiveFailingDeliveryContextService) Resolve(context.Context, string, ConversationRef) (*DeliveryContextRecord, error) {
	return nil, nil
}

func (f *selectiveFailingDeliveryContextService) ClearForConversation(_ context.Context, _ string, ref ConversationRef) error {
	if f.failConversationIDs[ref.ConversationID] {
		return f.err
	}
	return nil
}
