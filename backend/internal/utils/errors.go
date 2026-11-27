package utils

import "errors"

// Kinds of errors the API turns into HTTP statuses (see Fail).
var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrForbidden   = errors.New("forbidden")
	ErrInvalid     = errors.New("invalid request")
	ErrUnsupported = errors.New("unsupported file type")
	ErrTooLarge    = errors.New("file too large")
)

// UserError carries a message that is safe to show to the user, plus its kind (one of the Err… above).
type UserError struct {
	Kind error
	Msg  string
}

func (e *UserError) Error() string { return e.Msg }
func (e *UserError) Unwrap() error { return e.Kind }

// UserErr makes a UserError, e.g. UserErr(ErrConflict, "invoice is already approved").
func UserErr(kind error, msg string) error { return &UserError{kind, msg} }
