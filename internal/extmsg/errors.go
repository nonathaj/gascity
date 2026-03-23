package extmsg

import "errors"

var (
	ErrUnauthorized        = errors.New("extmsg unauthorized")
	ErrInvalidCaller       = errors.New("extmsg invalid caller")
	ErrInvalidInput        = errors.New("extmsg invalid input")
	ErrInvalidConversation = errors.New("extmsg invalid conversation")
	ErrInvalidHandle       = errors.New("extmsg invalid handle")
	ErrBindingConflict     = errors.New("extmsg binding conflict")
	ErrBindingMismatch     = errors.New("extmsg binding mismatch")
	ErrInvariantViolation  = errors.New("extmsg invariant violation")
	ErrGroupNotFound       = errors.New("extmsg group not found")
	ErrGroupRouteNotFound  = errors.New("extmsg group route not found")
)
