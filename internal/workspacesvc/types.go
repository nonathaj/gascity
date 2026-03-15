// Package workspacesvc provides the generic workspace-owned service runtime.
package workspacesvc

import (
	"context"
	"net/http"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/supervisor"
)

// Status is the API-facing state projection for one workspace service.
type Status struct {
	ServiceName      string `json:"service_name"`
	Kind             string `json:"kind,omitempty"`
	WorkflowContract string `json:"workflow_contract,omitempty"`
	MountPath        string `json:"mount_path"`
	PublishMode      string `json:"publish_mode"`
	Visibility       string `json:"visibility,omitempty"`
	Hostname         string `json:"hostname,omitempty"`
	StateRoot        string `json:"state_root"`
	// PublicURL is the legacy compatibility alias for URL.
	PublicURL string `json:"public_url,omitempty"`
	// URL is the canonical published-service URL.
	URL string `json:"url,omitempty"`
	// ServiceState is the legacy compatibility alias for State.
	ServiceState string `json:"service_state"`
	// State is the canonical service state.
	State            string `json:"state,omitempty"`
	LocalState       string `json:"local_state"`
	PublicationState string `json:"publication_state"`
	// StateReason is the legacy compatibility alias for Reason.
	StateReason string `json:"state_reason,omitempty"`
	// Reason is the canonical human/actionable reason for State.
	Reason          string    `json:"reason,omitempty"`
	AllowWebSockets bool      `json:"allow_websockets,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SetPublishedURL updates the canonical and compatibility URL fields together.
func (s *Status) SetPublishedURL(url string) {
	s.URL = url
	s.PublicURL = url
}

// SetState updates the canonical and compatibility state fields together.
func (s *Status) SetState(state string) {
	s.State = state
	s.ServiceState = state
}

// SetReason updates the canonical and compatibility reason fields together.
func (s *Status) SetReason(reason string) {
	s.Reason = reason
	s.StateReason = reason
}

// SyncAliases backfills whichever side of each compatibility pair is missing.
func (s *Status) SyncAliases() {
	if s.URL == "" {
		s.URL = s.PublicURL
	}
	if s.PublicURL == "" {
		s.PublicURL = s.URL
	}
	if s.State == "" {
		s.State = s.ServiceState
	}
	if s.ServiceState == "" {
		s.ServiceState = s.State
	}
	if s.Reason == "" {
		s.Reason = s.StateReason
	}
	if s.StateReason == "" {
		s.StateReason = s.Reason
	}
}

// RuntimeContext provides the runtime hooks a workspace service can use.
type RuntimeContext interface {
	CityPath() string
	CityName() string
	Config() *config.City
	PublicationConfig() supervisor.PublicationConfig
	SessionProvider() runtime.Provider
	BeadStore(rig string) beads.Store
	Poke()
}

// Instance is one runtime service implementation.
type Instance interface {
	Status() Status
	HandleHTTP(w http.ResponseWriter, r *http.Request, subpath string) bool
	Tick(ctx context.Context, now time.Time)
	Close() error
}

// Registry is the controller-owned workspace service registry.
type Registry interface {
	List() []Status
	Get(name string) (Status, bool)
	AuthorizeAndServeHTTP(name string, w http.ResponseWriter, r *http.Request, authorize func(Status) bool) bool
}

// WorkflowFactory constructs a workflow service for a known contract.
type WorkflowFactory func(rt RuntimeContext, svc config.Service) (Instance, error)
