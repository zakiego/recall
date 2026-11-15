// Package jserr gives Go errors the JavaScript error names the TS build
// recorded, so stored messages ("TypeError: fetch failed", "GatewayError: ...")
// read the same. jobs.last_error is `${error.name}: ${error.message}`.
package jserr

import "errors"

// Error is an error with a JS name.
type Error struct {
	Name    string
	Message string
	Cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

// JSName is the error's name.
func (e *Error) JSName() string { return e.Name }

// New returns an *Error.
func New(name, message string) *Error { return &Error{Name: name, Message: message} }

// Named is implemented by errors that carry a JS name.
type Named interface {
	error
	JSName() string
}

// Name is err.name: the JS name of the first named error in the chain, else
// "Error".
func Name(err error) string {
	var n Named
	if errors.As(err, &n) {
		return n.JSName()
	}
	return "Error"
}

// Format is `${error.name}: ${error.message}`.
func Format(err error) string {
	var n Named
	if errors.As(err, &n) {
		return n.JSName() + ": " + n.Error()
	}
	return "Error: " + err.Error()
}
