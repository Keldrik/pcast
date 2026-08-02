package model

import (
	"errors"
	"fmt"
)

// Stable symbolic error codes for automation.
const (
	CodeInvalidArgument   = "invalid_argument"
	CodeNotFound          = "not_found"
	CodeAmbiguousSelector = "ambiguous_selector"
	CodeFeedUnavailable   = "feed_unavailable"
	CodeInvalidFeed       = "invalid_feed"
	CodeStorageError      = "storage_error"
	CodeLockUnavailable   = "lock_unavailable"
	CodePlayerUnavailable = "player_unavailable"
	CodePlayerFailed      = "player_failed"
	CodeInternalError     = "internal_error"
)

// Exit statuses documented in project.md.
const (
	ExitOK            = 0
	ExitInternal      = 1
	ExitUsage         = 2
	ExitNotFound      = 3
	ExitNetwork       = 4
	ExitStorage       = 5
	ExitPlayer        = 6
	ExitPartialLatest = 4
)

// Error is a typed application error with a stable code and exit status.
type Error struct {
	Code    string
	Message string
	Details map[string]any
	Err     error
	Exit    int
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ExitCode returns the process exit status for err.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ae *Error
	if errors.As(err, &ae) && ae.Exit != 0 {
		return ae.Exit
	}
	return ExitInternal
}

// CodeOf returns the symbolic error code for err.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return CodeInternalError
}

// DetailsOf returns structured error details when present.
func DetailsOf(err error) map[string]any {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Details
	}
	return nil
}

func newError(code string, exit int, message string, err error, details map[string]any) *Error {
	if details == nil {
		details = map[string]any{}
	}
	return &Error{
		Code:    code,
		Message: message,
		Details: details,
		Err:     err,
		Exit:    exit,
	}
}

// InvalidArgument returns a usage error.
func InvalidArgument(message string) *Error {
	return newError(CodeInvalidArgument, ExitUsage, message, nil, nil)
}

// InvalidArgumentf formats a usage error.
func InvalidArgumentf(format string, args ...any) *Error {
	return InvalidArgument(fmt.Sprintf(format, args...))
}

// NotFound returns a missing-entity error.
func NotFound(message string) *Error {
	return newError(CodeNotFound, ExitNotFound, message, nil, nil)
}

// NotFoundf formats a missing-entity error.
func NotFoundf(format string, args ...any) *Error {
	return NotFound(fmt.Sprintf(format, args...))
}

// AmbiguousSelector returns an ambiguous podcast selector error.
func AmbiguousSelector(message string, candidates []int64) *Error {
	ids := make([]any, len(candidates))
	for i, id := range candidates {
		ids[i] = id
	}
	return newError(CodeAmbiguousSelector, ExitNotFound, message, nil, map[string]any{
		"candidates": ids,
	})
}

// FeedUnavailable returns a network/feed transport error.
func FeedUnavailable(message string, err error) *Error {
	return newError(CodeFeedUnavailable, ExitNetwork, message, err, nil)
}

// InvalidFeed returns a feed parsing/validation error.
func InvalidFeed(message string, err error) *Error {
	return newError(CodeInvalidFeed, ExitNetwork, message, err, nil)
}

// Storage returns a storage/migration error.
func Storage(message string, err error) *Error {
	return newError(CodeStorageError, ExitStorage, message, err, nil)
}

// LockUnavailable returns a lock acquisition error.
func LockUnavailable(message string, err error) *Error {
	return newError(CodeLockUnavailable, ExitStorage, message, err, nil)
}

// PlayerUnavailable returns a missing-player error.
func PlayerUnavailable(message string) *Error {
	return newError(CodePlayerUnavailable, ExitPlayer, message, nil, nil)
}

// PlayerFailed returns a playback failure error.
func PlayerFailed(message string, err error) *Error {
	return newError(CodePlayerFailed, ExitPlayer, message, err, nil)
}

// Internal returns an unexpected internal error.
func Internal(message string, err error) *Error {
	return newError(CodeInternalError, ExitInternal, message, err, nil)
}

// Wrap preserves a typed Error code when present; otherwise wraps as internal.
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return &Error{
			Code:    ae.Code,
			Message: message,
			Details: ae.Details,
			Err:     err,
			Exit:    ae.Exit,
		}
	}
	return Internal(message, err)
}
