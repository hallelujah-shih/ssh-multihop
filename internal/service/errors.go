package service

import "errors"

// Standard errors for service operations
var (
	// ErrForwardNotFound is returned when a forward does not exist
	ErrForwardNotFound = errors.New("forward not found")

	// ErrStatusNotFound is returned when a status does not exist
	ErrStatusNotFound = errors.New("status not found")
)
