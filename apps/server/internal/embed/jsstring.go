package embed

import (
	"strings"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

func trimStartJS(s string) string { return strings.TrimLeftFunc(s, strx.IsSpace) }
func trimEndJS(s string) string   { return strings.TrimRightFunc(s, strx.IsSpace) }

// CollapseSpace is text.replace(/\s+/g, " ").trim() in JavaScript.
func CollapseSpace(s string) string {
	return strx.Trim(strx.CollapseSpace(s))
}
