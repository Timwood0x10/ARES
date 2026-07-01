// Package errors provides high-performance error wrapping utilities.

package errors

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for common domain conditions.
var (
	// ErrNotFound indicates the requested entity does not exist.
	// Domain-specific variants should wrap this with fmt.Errorf("%w: context", ErrNotFound).
	ErrNotFound = errors.New("not found")
)

// New creates a new error with the given message.
func New(message string) error {
	return &wrappedError{
		msg: message,
		err: nil,
	}
}

// Newf creates a new error with a formatted message.
func Newf(format string, args ...any) error {
	return &wrappedError{
		msg: fmt.Sprintf(format, args...),
		err: nil,
	}
}

// Wrap wraps an error with a context message without format string parsing.
// This is more efficient than fmt.Errorf for high-frequency error paths.
//
// Usage:
//
//	return Wrap(err, "operation name")
//	return Wrap(err, "operation name: additional context")
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	if message == "" {
		return err
	}
	return &wrappedError{
		msg: message,
		err: err,
	}
}

// WrapError wraps an error with another error (for %w: %w pattern).
// This is used when you want to chain two errors together.
func WrapError(baseErr, wrapErr error) error {
	if wrapErr == nil {
		return baseErr
	}
	if baseErr == nil {
		return wrapErr
	}
	return &wrappedError{
		msg: baseErr.Error(),
		err: wrapErr,
	}
}

// Wrapf wraps an error with a formatted message (use sparingly).
// This should only be used when format string is necessary.
// For simple concatenation, use Wrap instead.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}

// FormatError creates a new error with a formatted message using %w for error wrapping.
// This is used when you want to format an error with additional context.
func FormatError(baseErr error, format string, args ...any) error {
	if baseErr == nil {
		return nil
	}
	// Check if format string contains %w
	if strings.Contains(format, "%w") {
		// Replace %w with %s for formatting, then wrap the error
		formatWithoutW := strings.ReplaceAll(format, "%w", "%s")
		message := fmt.Sprintf(formatWithoutW, append(args, baseErr.Error())...)
		return &wrappedError{
			msg: message,
			err: baseErr,
		}
	}
	// If no %w, just format the message
	message := fmt.Sprintf(format, args...)
	return &wrappedError{
		msg: message,
		err: baseErr,
	}
}

// wrappedError is a lightweight error wrapper.
type wrappedError struct {
	msg string
	err error
}

func (w *wrappedError) Error() string {
	if w.err == nil {
		return w.msg
	}
	var b strings.Builder
	b.Grow(len(w.msg) + 2 + len(w.err.Error()))
	b.WriteString(w.msg)
	b.WriteString(": ")
	b.WriteString(w.err.Error())
	return b.String()
}

func (w *wrappedError) Unwrap() error {
	return w.err
}
