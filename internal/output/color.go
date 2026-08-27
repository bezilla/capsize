package output

import (
	"io"
	"os"
	"strings"
)

// palette is a tiny ANSI helper. capsize has no color dependency: a report
// that has to be pasted into a ticket should not carry a rendering library.
type palette struct{ on bool }

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[36m"
	ansiGray   = "\x1b[90m"
)

func (p palette) wrap(code, s string) string {
	if !p.on || s == "" {
		return s
	}
	return code + s + ansiReset
}

func (p palette) bold(s string) string   { return p.wrap(ansiBold, s) }
func (p palette) dim(s string) string    { return p.wrap(ansiDim, s) }
func (p palette) red(s string) string    { return p.wrap(ansiRed, s) }
func (p palette) yellow(s string) string { return p.wrap(ansiYellow, s) }
func (p palette) blue(s string) string   { return p.wrap(ansiBlue, s) }
func (p palette) gray(s string) string   { return p.wrap(ansiGray, s) }

// width returns the printable width of s, ignoring escape sequences, so
// tabwriter padding stays correct when color is on.
func width(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case esc && r == 'm':
			esc = false
		case esc:
		case r == '\x1b':
			esc = true
		default:
			n++
		}
	}
	return n
}

// pad right-pads s to n printable columns.
func pad(s string, n int) string {
	if d := n - width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// colorEnabled decides whether to emit escape sequences: never when the
// caller asked for --no-color, never when NO_COLOR is set (the informal
// standard), and never when the destination is not a terminal, so a piped or
// redirected report stays clean.
func colorEnabled(w io.Writer, disabled bool) bool {
	if disabled || os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
