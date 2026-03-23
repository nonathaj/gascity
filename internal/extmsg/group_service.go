package extmsg

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
)

type groupService struct {
	store beads.Store
	locks *bindingLockPool
}

func NewGroupService(store beads.Store) GroupService {
	return newGroupService(store, sharedBindingLockPool(store))
}

func newGroupService(store beads.Store, locks *bindingLockPool) GroupService {
	return &groupService{store: store, locks: locks}
}

func (s *groupService) EnsureGroup(ctx context.Context, caller Caller, input EnsureGroupInput) (ConversationGroupRecord, error) {
	if err := checkContext(ctx); err != nil {
		return ConversationGroupRecord{}, err
	}
	ref, err := validateConversationRef(input.RootConversation)
	if err != nil {
		return ConversationGroupRecord{}, err
	}
	if err := authorizeMutation(caller, ref); err != nil {
		return ConversationGroupRecord{}, err
	}
	mode := GroupMode(strings.ToLower(strings.TrimSpace(string(input.Mode))))
	switch mode {
	case GroupModeLauncher:
	default:
		return ConversationGroupRecord{}, fmt.Errorf("%w: invalid group mode %q", ErrInvalidInput, input.Mode)
	}
	defaultHandle := normalizeHandle(input.DefaultHandle)
	lastHandle := normalizeHandle(input.LastAddressedHandle)
	title := conversationTitle(ref)
	fields := encodeMetadataFields(input.Metadata, map[string]string{
		"schema_version":                      strconv.Itoa(schemaVersion),
		"scope_id":                            ref.ScopeID,
		"provider":                            ref.Provider,
		"account_id":                          ref.AccountID,
		"conversation_id":                     ref.ConversationID,
		"parent_conversation_id":              ref.ParentConversationID,
		"conversation_kind":                   string(ref.Kind),
		"mode":                                string(mode),
		"default_handle":                      defaultHandle,
		"last_addressed_handle":               lastHandle,
		"ambient_read_enabled":                strconv.FormatBool(input.AmbientReadEnabled),
		"fanout_enabled":                      strconv.FormatBool(input.FanoutPolicy.Enabled),
		"fanout_allow_untargeted":             strconv.FormatBool(input.FanoutPolicy.AllowUntargetedPublication),
		"fanout_max_peer_triggered_publishes": strconv.Itoa(input.FanoutPolicy.MaxPeerTriggeredPublishes),
		"fanout_max_total_peer_deliveries":    strconv.Itoa(input.FanoutPolicy.MaxTotalPeerDeliveries),
	})
	if lastHandle == "" {
		delete(fields, "last_addressed_handle")
	}
	var out ConversationGroupRecord
	err = withLockKey(s.locks, groupRootLabel(ref), func() error {
		items, err := s.store.ListByLabel(groupRootLabel(ref), 0)
		if err != nil {
			return fmt.Errorf("list groups by root label: %w", err)
		}
		for _, item := range items {
			if err := checkContext(ctx); err != nil {
				return err
			}
			if item.Type != "external_group" || item.Status == "closed" {
				continue
			}
			record, err := decodeGroupBead(item)
			if err != nil {
				return err
			}
			if !sameConversationRef(record.RootConversation, ref) {
				continue
			}
			if err := s.store.Update(item.ID, beads.UpdateOpts{Title: &title}); err != nil {
				return fmt.Errorf("update group title: %w", err)
			}
			if err := s.store.SetMetadataBatch(item.ID, fields); err != nil {
				return fmt.Errorf("update group metadata: %w", err)
			}
			updated, err := s.store.Get(item.ID)
			if err != nil {
				return fmt.Errorf("get group %s: %w", item.ID, err)
			}
			out, err = decodeGroupBead(updated)
			return err
		}
		created, err := s.store.Create(beads.Bead{
			Title:    title,
			Type:     "external_group",
			Labels:   []string{labelGroupBase, groupRootLabel(ref)},
			Metadata: fields,
		})
		if err != nil {
			return fmt.Errorf("create group: %w", err)
		}
		out, err = decodeGroupBead(created)
		return err
	})
	return out, err
}

func (s *groupService) UpsertParticipant(ctx context.Context, caller Caller, input UpsertParticipantInput) (ConversationGroupParticipant, error) {
	if err := checkContext(ctx); err != nil {
		return ConversationGroupParticipant{}, err
	}
	groupID := strings.TrimSpace(input.GroupID)
	if groupID == "" {
		return ConversationGroupParticipant{}, fmt.Errorf("%w: group_id required", ErrInvalidInput)
	}
	handle, err := validateHandle(input.Handle)
	if err != nil {
		return ConversationGroupParticipant{}, err
	}
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return ConversationGroupParticipant{}, fmt.Errorf("%w: session_id required", ErrInvalidInput)
	}
	group, err := s.getGroupByID(groupID)
	if err != nil {
		return ConversationGroupParticipant{}, err
	}
	if err := authorizeMutation(caller, group.RootConversation); err != nil {
		return ConversationGroupParticipant{}, err
	}
	title := groupID + "/" + handle
	fields := encodeMetadataFields(input.Metadata, map[string]string{
		"schema_version": strconv.Itoa(schemaVersion),
		"group_id":       groupID,
		"handle":         handle,
		"session_id":     sessionID,
		"public":         strconv.FormatBool(input.Public),
	})
	var out ConversationGroupParticipant
	err = withLockKey(s.locks, groupParticipantLabel(groupID)+":"+handle, func() error {
		items, err := s.store.ListByLabel(groupParticipantLabel(groupID), 0)
		if err != nil {
			return fmt.Errorf("list group participants: %w", err)
		}
		for _, item := range items {
			if err := checkContext(ctx); err != nil {
				return err
			}
			if item.Type != "external_group_participant" || item.Status == "closed" {
				continue
			}
			record, err := decodeParticipantBead(item)
			if err != nil {
				return err
			}
			if record.Handle != handle {
				continue
			}
			labelsToAdd, labelsToRemove := recordLabels(item.Labels, []string{groupParticipantSessionLabel(record.SessionID)}, []string{groupParticipantSessionLabel(sessionID)})
			if err := s.store.Update(item.ID, beads.UpdateOpts{
				Title:        &title,
				Labels:       labelsToAdd,
				RemoveLabels: labelsToRemove,
			}); err != nil {
				return fmt.Errorf("update group participant: %w", err)
			}
			if err := s.store.SetMetadataBatch(item.ID, fields); err != nil {
				return fmt.Errorf("update participant metadata: %w", err)
			}
			updated, err := s.store.Get(item.ID)
			if err != nil {
				return fmt.Errorf("get participant %s: %w", item.ID, err)
			}
			out, err = decodeParticipantBead(updated)
			return err
		}
		created, err := s.store.Create(beads.Bead{
			Title:    title,
			Type:     "external_group_participant",
			Labels:   []string{labelGroupParticipantBase, groupParticipantLabel(groupID), groupParticipantSessionLabel(sessionID)},
			Metadata: fields,
		})
		if err != nil {
			return fmt.Errorf("create group participant: %w", err)
		}
		out, err = decodeParticipantBead(created)
		return err
	})
	return out, err
}

func (s *groupService) ResolveInbound(ctx context.Context, event ExternalInboundMessage) (*GroupRouteDecision, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	ref, err := validateConversationRef(event.Conversation)
	if err != nil {
		return nil, err
	}
	group, err := s.findGroupByRoot(ref)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return &GroupRouteDecision{Match: GroupRouteNoMatch}, nil
	}
	participants, err := s.listParticipants(group.ID)
	if err != nil {
		return nil, err
	}
	byHandle := make(map[string]ConversationGroupParticipant, len(participants))
	for _, participant := range participants {
		byHandle[participant.Handle] = participant
	}
	resolvePassive := func(targetSession string) []string {
		if !group.AmbientReadEnabled {
			return nil
		}
		passive := make([]string, 0, len(participants)-1)
		for _, participant := range participants {
			if participant.SessionID == targetSession {
				continue
			}
			passive = append(passive, participant.SessionID)
		}
		return dedupeStrings(passive)
	}
	if explicit := normalizeHandle(event.ExplicitTarget); explicit != "" {
		target, ok := byHandle[explicit]
		if !ok {
			return &GroupRouteDecision{Match: GroupRouteNoMatch}, nil
		}
		return &GroupRouteDecision{
			Match:             GroupRouteExplicitTarget,
			TargetSessionID:   target.SessionID,
			PassiveSessionIDs: resolvePassive(target.SessionID),
			UpdateCursor:      true,
		}, nil
	}
	if target, ok := byHandle[group.LastAddressedHandle]; ok {
		return &GroupRouteDecision{
			Match:             GroupRouteLastAddressed,
			TargetSessionID:   target.SessionID,
			PassiveSessionIDs: resolvePassive(target.SessionID),
		}, nil
	}
	if target, ok := byHandle[group.DefaultHandle]; ok {
		return &GroupRouteDecision{
			Match:             GroupRouteDefault,
			TargetSessionID:   target.SessionID,
			PassiveSessionIDs: resolvePassive(target.SessionID),
		}, nil
	}
	return &GroupRouteDecision{Match: GroupRouteNoMatch}, nil
}

func (s *groupService) UpdateCursor(ctx context.Context, caller Caller, input UpdateCursorInput) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	ref, err := validateConversationRef(input.RootConversation)
	if err != nil {
		return err
	}
	if err := authorizeMutation(caller, ref); err != nil {
		return err
	}
	handle := normalizeHandle(input.Handle)
	group, err := s.findGroupByRoot(ref)
	if err != nil {
		return err
	}
	if group == nil {
		return ErrGroupNotFound
	}
	if handle == "" {
		return s.store.SetMetadata(group.ID, "last_addressed_handle", "")
	}
	participants, err := s.listParticipants(group.ID)
	if err != nil {
		return err
	}
	found := false
	for _, participant := range participants {
		if participant.Handle == handle {
			found = true
			break
		}
	}
	if !found {
		return ErrGroupRouteNotFound
	}
	return s.store.SetMetadata(group.ID, "last_addressed_handle", handle)
}

func (s *groupService) findGroupByRoot(ref ConversationRef) (*ConversationGroupRecord, error) {
	items, err := s.store.ListByLabel(groupRootLabel(ref), 0)
	if err != nil {
		return nil, fmt.Errorf("list groups by root label: %w", err)
	}
	var out *ConversationGroupRecord
	for _, item := range items {
		if item.Type != "external_group" || item.Status == "closed" {
			continue
		}
		record, err := decodeGroupBead(item)
		if err != nil {
			return nil, err
		}
		if !sameConversationRef(record.RootConversation, ref) {
			continue
		}
		if out != nil {
			return nil, fmt.Errorf("%w: multiple groups for %s", ErrInvariantViolation, conversationLockKey(ref))
		}
		copy := record
		out = &copy
	}
	return out, nil
}

func (s *groupService) getGroupByID(groupID string) (ConversationGroupRecord, error) {
	item, err := s.store.Get(groupID)
	if err != nil {
		return ConversationGroupRecord{}, fmt.Errorf("get group %s: %w", groupID, err)
	}
	if item.Type != "external_group" || item.Status == "closed" {
		return ConversationGroupRecord{}, ErrGroupNotFound
	}
	return decodeGroupBead(item)
}

func (s *groupService) listParticipants(groupID string) ([]ConversationGroupParticipant, error) {
	items, err := s.store.ListByLabel(groupParticipantLabel(groupID), 0)
	if err != nil {
		return nil, fmt.Errorf("list group participants: %w", err)
	}
	out := make([]ConversationGroupParticipant, 0, len(items))
	seen := make(map[string]ConversationGroupParticipant)
	for _, item := range items {
		if item.Type != "external_group_participant" || item.Status == "closed" {
			continue
		}
		record, err := decodeParticipantBead(item)
		if err != nil {
			return nil, err
		}
		if existing, ok := seen[record.Handle]; ok {
			return nil, fmt.Errorf("%w: duplicate participants for handle %s (%s, %s)", ErrInvariantViolation, record.Handle, existing.ID, record.ID)
		}
		seen[record.Handle] = record
		out = append(out, record)
	}
	return out, nil
}

func decodeGroupBead(b beads.Bead) (ConversationGroupRecord, error) {
	ref, err := conversationRefFromMetadata(b.Metadata)
	if err != nil {
		return ConversationGroupRecord{}, err
	}
	return ConversationGroupRecord{
		ID:                  b.ID,
		SchemaVersion:       parseInt(b.Metadata, "schema_version"),
		RootConversation:    ref,
		Mode:                GroupMode(strings.TrimSpace(b.Metadata["mode"])),
		DefaultHandle:       normalizeHandle(b.Metadata["default_handle"]),
		LastAddressedHandle: normalizeHandle(b.Metadata["last_addressed_handle"]),
		AmbientReadEnabled:  parseBool(b.Metadata, "ambient_read_enabled"),
		FanoutPolicy: FanoutPolicy{
			Enabled:                    parseBool(b.Metadata, "fanout_enabled"),
			AllowUntargetedPublication: parseBool(b.Metadata, "fanout_allow_untargeted"),
			MaxPeerTriggeredPublishes:  parseInt(b.Metadata, "fanout_max_peer_triggered_publishes"),
			MaxTotalPeerDeliveries:     parseInt(b.Metadata, "fanout_max_total_peer_deliveries"),
		},
		Metadata: decodePrefixedMetadata(b.Metadata),
	}, nil
}

func decodeParticipantBead(b beads.Bead) (ConversationGroupParticipant, error) {
	return ConversationGroupParticipant{
		ID:        b.ID,
		GroupID:   strings.TrimSpace(b.Metadata["group_id"]),
		Handle:    normalizeHandle(b.Metadata["handle"]),
		SessionID: strings.TrimSpace(b.Metadata["session_id"]),
		Public:    parseBool(b.Metadata, "public"),
		Metadata:  decodePrefixedMetadata(b.Metadata),
	}, nil
}
