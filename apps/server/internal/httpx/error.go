// Package httpx holds the HTTP error type the domain code throws (HttpError
// in http.ts). The handlers in internal/httpapi turn it into a JSON error.
package httpx

// Error is an error with an HTTP status, an error type for the JSON body and
// extra response headers.
type Error struct {
	Status  int
	Type    string
	Message string
	Headers map[string]string
}

func (e *Error) Error() string { return e.Message }

// New returns an *Error.
func New(status int, typ, message string) *Error {
	return &Error{Status: status, Type: typ, Message: message, Headers: map[string]string{}}
}

// WithHeaders returns an *Error with response headers.
func WithHeaders(status int, typ, message string, headers map[string]string) *Error {
	return &Error{Status: status, Type: typ, Message: message, Headers: headers}
}
