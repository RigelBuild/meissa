// SPDX-License-Identifier: MIT OR Apache-2.0

// Package errs holds the error values the length gate prints as written.
package errs

import "fmt"

// MessageError is a failure whose text the CLI prints as written.
type MessageError string

func (e MessageError) Error() string { return string(e) }

// Messagef formats a failure message the CLI prints as written.
func Messagef(format string, args ...any) error {
	return MessageError(fmt.Sprintf(format, args...))
}
