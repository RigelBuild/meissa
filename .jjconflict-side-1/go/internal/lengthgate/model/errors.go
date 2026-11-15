// SPDX-License-Identifier: MIT OR Apache-2.0

package model

import "fmt"

// MessageError preserves diagnostics that the CLI prints verbatim.
type MessageError string

func (e MessageError) Error() string { return string(e) }

// Messagef formats a diagnostic that the CLI prints verbatim.
func Messagef(format string, args ...any) error {
	return MessageError(fmt.Sprintf(format, args...))
}
