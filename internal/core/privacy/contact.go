// Package privacy contains shared privacy-preserving presentation helpers.
package privacy

import "strings"

// MaskContact returns a trimmed contact value with every rune except the first
// and last replaced by an asterisk. Short and empty values are fully masked.
func MaskContact(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= 2 {
		return "**"
	}
	return string(runes[:1]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1:])
}
