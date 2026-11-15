// Package usernames holds the username rules (usernames.ts).
package usernames

import (
	"fmt"
	"regexp"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	Min = 3
	Max = 32
)

// Pattern is /^[a-zA-Z0-9_.]+$/.
var Pattern = regexp.MustCompile(`^[a-zA-Z0-9_.]+$`)

// Normalize is u.trim().toLowerCase().
func Normalize(u string) string { return strx.Lower(strx.Trim(u)) }

// Problem returns what is wrong with a username, or "" when it is fine.
func Problem(u string) string {
	t := strx.Trim(u)
	if strx.Len16(t) < Min {
		return fmt.Sprintf("Use at least %d characters.", Min)
	}
	if strx.Len16(t) > Max {
		return fmt.Sprintf("Use at most %d characters.", Max)
	}
	if !Pattern.MatchString(t) {
		return "Use letters, numbers, dots and underscores only."
	}
	return ""
}

// PlaceholderEmail is the email Better Auth requires on every user, under
// .invalid, a TLD reserved to never resolve.
func PlaceholderEmail(username string) string {
	return Normalize(username) + "@users.recall.invalid"
}
