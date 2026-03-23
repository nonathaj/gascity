package extmsg

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const (
	labelBindingBase          = "gc:extmsg-binding"
	labelDeliveryBase         = "gc:extmsg-delivery"
	labelGroupBase            = "gc:extmsg-group"
	labelGroupParticipantBase = "gc:extmsg-group-participant"

	labelBindingConversationPrefix = "extmsg:binding:conv:v1:"
	labelBindingSessionPrefix      = "extmsg:binding:session:v1:"
	labelDeliveryRoutePrefix       = "extmsg:delivery:route:v1:"
	labelDeliverySessionPrefix     = "extmsg:delivery:session:v1:"
	labelGroupRootPrefix           = "extmsg:group:root:v1:"
	labelGroupParticipantPrefix    = "extmsg:group:participant:v1:"
	labelGroupParticipantSession   = "extmsg:group:participant:session:v1:"
)

func bindingConversationLabel(ref ConversationRef) string {
	ref = normalizeConversationRef(ref)
	return labelBindingConversationPrefix + hashJoin(
		ref.ScopeID,
		ref.Provider,
		ref.AccountID,
		ref.ConversationID,
		ref.ParentConversationID,
		string(ref.Kind),
	)
}

func bindingSessionLabel(sessionID string) string {
	return labelBindingSessionPrefix + strings.TrimSpace(sessionID)
}

func deliveryRouteLabel(ref ConversationRef, sessionID string) string {
	ref = normalizeConversationRef(ref)
	return labelDeliveryRoutePrefix + hashJoin(
		ref.ScopeID,
		ref.Provider,
		ref.AccountID,
		ref.ConversationID,
		ref.ParentConversationID,
		string(ref.Kind),
		strings.TrimSpace(sessionID),
	)
}

func deliverySessionLabel(sessionID string) string {
	return labelDeliverySessionPrefix + strings.TrimSpace(sessionID)
}

func groupRootLabel(ref ConversationRef) string {
	ref = normalizeConversationRef(ref)
	return labelGroupRootPrefix + hashJoin(
		ref.ScopeID,
		ref.Provider,
		ref.AccountID,
		ref.ConversationID,
		ref.ParentConversationID,
		string(ref.Kind),
	)
}

func groupParticipantLabel(groupID string) string {
	return labelGroupParticipantPrefix + strings.TrimSpace(groupID)
}

func groupParticipantSessionLabel(sessionID string) string {
	return labelGroupParticipantSession + strings.TrimSpace(sessionID)
}

func conversationLockKey(ref ConversationRef) string {
	return bindingConversationLabel(ref)
}

func hashJoin(parts ...string) string {
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
