package extmsg

import "github.com/gastownhall/gascity/internal/beads"

// Services bundles the Phase 1 fabric services built over a shared lock pool.
type Services struct {
	Bindings BindingService
	Delivery DeliveryContextService
	Groups   GroupService
}

// NewServices creates binding, delivery, and group services that share the
// same per-fabric binding lock pool.
func NewServices(store beads.Store, opts ...BindingServiceOption) Services {
	locks := sharedBindingLockPool(store)
	delivery := newDeliveryContextService(store, locks)
	return Services{
		Bindings: newBindingService(store, delivery, locks, opts...),
		Delivery: delivery,
		Groups:   newGroupService(store, locks),
	}
}
