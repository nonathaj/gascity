package extmsg

import (
	"context"
	"errors"
	"time"
)

const (
	schemaVersion  = 1
	metadataPrefix = "meta."
)

type CallerKind string

const (
	CallerController CallerKind = "controller"
	CallerAdapter    CallerKind = "adapter"
)

type Caller struct {
	Kind      CallerKind
	ID        string
	Provider  string
	AccountID string
}

type ConversationKind string

const (
	ConversationDM     ConversationKind = "dm"
	ConversationRoom   ConversationKind = "room"
	ConversationThread ConversationKind = "thread"
)

type ConversationRef struct {
	ScopeID              string
	Provider             string
	AccountID            string
	ConversationID       string
	ParentConversationID string
	Kind                 ConversationKind
}

type InboundPayload struct {
	Body        []byte
	ContentType string
	Headers     map[string][]string
	ReceivedAt  time.Time
}

type ExternalActor struct {
	ID          string
	DisplayName string
	IsBot       bool
}

type ExternalAttachment struct {
	ProviderID string
	URL        string
	MIMEType   string
}

type ExternalInboundMessage struct {
	ProviderMessageID string
	Conversation      ConversationRef
	Actor             ExternalActor
	Text              string
	ExplicitTarget    string
	ReplyToMessageID  string
	Attachments       []ExternalAttachment
	DedupKey          string
	ReceivedAt        time.Time
}

type BindingStatus string

const (
	BindingActive BindingStatus = "active"
	BindingEnded  BindingStatus = "ended"
)

type SessionBindingRecord struct {
	ID                string
	SchemaVersion     int
	Conversation      ConversationRef
	SessionID         string
	Status            BindingStatus
	BoundAt           time.Time
	ExpiresAt         *time.Time
	BindingGeneration int64
	Metadata          map[string]string
}

type DeliveryContextRecord struct {
	ID                string
	SchemaVersion     int
	SessionID         string
	Conversation      ConversationRef
	BindingGeneration int64
	LastPublishedAt   time.Time
	LastMessageID     string
	SourceSessionID   string
	Metadata          map[string]string
}

type ExternalOriginEnvelope struct {
	Conversation      ConversationRef
	BindingID         string
	BindingGeneration int64
	Passive           bool
}

type AdapterCapabilities struct {
	SupportsChildConversations bool
	SupportsAttachments        bool
	MaxMessageLength           int
}

type PublishRequest struct {
	Conversation     ConversationRef
	Text             string
	ReplyToMessageID string
	IdempotencyKey   string
	Metadata         map[string]string
}

type PublishFailureKind string

const (
	PublishFailureUnsupported PublishFailureKind = "unsupported"
	PublishFailureTransient   PublishFailureKind = "transient"
	PublishFailureRateLimited PublishFailureKind = "rate_limited"
	PublishFailurePermanent   PublishFailureKind = "permanent"
	PublishFailureAuth        PublishFailureKind = "auth"
	PublishFailureNotFound    PublishFailureKind = "not_found"
)

type PublishReceipt struct {
	MessageID    string
	Conversation ConversationRef
	Delivered    bool
	FailureKind  PublishFailureKind
	RetryAfter   time.Duration
	Metadata     map[string]string
}

var ErrAdapterUnsupported = errors.New("adapter unsupported")

type GroupMode string

const (
	GroupModeLauncher GroupMode = "launcher"
)

type FanoutPolicy struct {
	Enabled                    bool
	AllowUntargetedPublication bool
	MaxPeerTriggeredPublishes  int
	MaxTotalPeerDeliveries     int
}

type ConversationGroupRecord struct {
	ID                  string
	SchemaVersion       int
	RootConversation    ConversationRef
	Mode                GroupMode
	DefaultHandle       string
	LastAddressedHandle string
	AmbientReadEnabled  bool
	FanoutPolicy        FanoutPolicy
	Metadata            map[string]string
}

type ConversationGroupParticipant struct {
	ID        string
	GroupID   string
	Handle    string
	SessionID string
	Public    bool
	Metadata  map[string]string
}

type GroupRouteMatch string

const (
	GroupRouteExplicitTarget GroupRouteMatch = "explicit_target"
	GroupRouteLastAddressed  GroupRouteMatch = "last_addressed"
	GroupRouteDefault        GroupRouteMatch = "default"
	GroupRouteNoMatch        GroupRouteMatch = "no_match"
)

type GroupRouteDecision struct {
	Match             GroupRouteMatch
	TargetSessionID   string
	PassiveSessionIDs []string
	UpdateCursor      bool
}

type BindInput struct {
	Conversation ConversationRef
	SessionID    string
	ExpiresAt    *time.Time
	Metadata     map[string]string
	Now          time.Time
}

type UnbindInput struct {
	Conversation *ConversationRef
	SessionID    string
	Now          time.Time
}

type EnsureGroupInput struct {
	RootConversation    ConversationRef
	Mode                GroupMode
	DefaultHandle       string
	LastAddressedHandle string
	AmbientReadEnabled  bool
	FanoutPolicy        FanoutPolicy
	Metadata            map[string]string
}

type UpsertParticipantInput struct {
	GroupID   string
	Handle    string
	SessionID string
	Public    bool
	Metadata  map[string]string
}

type UpdateCursorInput struct {
	RootConversation ConversationRef
	Handle           string
}

type BindingService interface {
	Bind(ctx context.Context, caller Caller, input BindInput) (SessionBindingRecord, error)
	ResolveByConversation(ctx context.Context, ref ConversationRef) (*SessionBindingRecord, error)
	ListBySession(ctx context.Context, sessionID string) ([]SessionBindingRecord, error)
	Touch(ctx context.Context, caller Caller, bindingID string, now time.Time) error
	Unbind(ctx context.Context, caller Caller, input UnbindInput) ([]SessionBindingRecord, error)
}

type DeliveryContextService interface {
	Record(ctx context.Context, caller Caller, input DeliveryContextRecord) error
	Resolve(ctx context.Context, sessionID string, ref ConversationRef) (*DeliveryContextRecord, error)
	ClearForConversation(ctx context.Context, sessionID string, ref ConversationRef) error
}

type GroupService interface {
	EnsureGroup(ctx context.Context, caller Caller, input EnsureGroupInput) (ConversationGroupRecord, error)
	UpsertParticipant(ctx context.Context, caller Caller, input UpsertParticipantInput) (ConversationGroupParticipant, error)
	ResolveInbound(ctx context.Context, event ExternalInboundMessage) (*GroupRouteDecision, error)
	UpdateCursor(ctx context.Context, caller Caller, input UpdateCursorInput) error
}

type TransportAdapter interface {
	Name() string
	Capabilities() AdapterCapabilities
	VerifyAndNormalizeInbound(ctx context.Context, payload InboundPayload) (*ExternalInboundMessage, error)
	Publish(ctx context.Context, req PublishRequest) (*PublishReceipt, error)
	EnsureChildConversation(ctx context.Context, ref ConversationRef, label string) (*ConversationRef, error)
}
