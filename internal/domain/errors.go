package domain

import "errors"

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrRateLimited   = errors.New("rate limited")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrInvalidOrder  = errors.New("invalid order parameters")
	ErrSigningFailed = errors.New("signing failed")
	ErrWSDisconnect  = errors.New("websocket disconnected")
	ErrContextDone   = errors.New("context cancelled")
	ErrLockHeld      = errors.New("lock already held")
)
