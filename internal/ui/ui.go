// Package ui provides small terminal output helpers: color (TTY-aware),
// status markers, aligned tables, and global output-mode flags (--json / --quiet).
package ui

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// Global output modes, set from CLI flags.
var (
	JSON  bool
	Quiet bool
)

// isTTY reports whether f is a character device (an interactive terminal).
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// color is enabled only on a TTY and when NO_COLOR is unset.
var color = isTTY(os.Stdout) && os.Getenv("NO_COLOR") == ""

// Status markers — plain ASCII, no emoji.
const (
	SymOK    = "ok"
	SymWarn  = "!"
	SymErr   = "x"
	SymDash  = "-"
	SymDot   = "*"
	SymRing  = "-"
	SymArrow = "->"
)

func wrap(code, s string) string {
	if !color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func Bold(s string) string   { return wrap("1", s) }
func Dim(s string) string    { return wrap("2", s) }
func Red(s string) string    { return wrap("31", s) }
func Green(s string) string  { return wrap("32", s) }
func Yellow(s string) string { return wrap("33", s) }
func Blue(s string) string   { return wrap("34", s) }
func Cyan(s string) string   { return wrap("36", s) }

// IsTTY reports whether stdout is an interactive terminal.
func IsTTY() bool { return isTTY(os.Stdout) }

// Printf writes to stdout unless --quiet is set.
func Printf(format string, a ...any) {
	if Quiet {
		return
	}
	fmt.Printf(format, a...)
}

// Println writes a line to stdout unless --quiet is set.
func Println(a ...any) {
	if Quiet {
		return
	}
	fmt.Println(a...)
}

// Errf writes to stderr (always, regardless of --quiet).
func Errf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}

// TermWidth returns the terminal width in columns, or 80 when it can't be
// determined (e.g. output is piped).
func TermWidth() int {
	type winsize struct{ Row, Col, Xpix, Ypix uint16 }
	ws := &winsize{}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(ws)))
	if errno != 0 || ws.Col == 0 {
		return 80
	}
	return int(ws.Col)
}

// VisibleLen returns the rune width of s, ignoring ANSI color escape sequences
// so padded columns line up regardless of coloring.
func VisibleLen(s string) int {
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == '\033':
			inEsc = true
		default:
			n++
		}
	}
	return n
}

// Truncate shortens plain (uncolored) text to at most n visible columns,
// appending "..." when it has to cut.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

// Table renders rows as left-aligned columns, sizing each column to its widest
// cell and separating columns with a fixed gap. Cells may contain ANSI color.
type Table struct {
	indent string
	gap    int
	rows   [][]string
}

// NewTable returns a table indented two spaces with a two-space column gap.
func NewTable() *Table { return &Table{indent: "  ", gap: 2} }

// Indent overrides the leading indent for every row.
func (t *Table) Indent(s string) *Table { t.indent = s; return t }

// Row appends a row of cells.
func (t *Table) Row(cells ...string) *Table {
	t.rows = append(t.rows, cells)
	return t
}

// Render prints the table with each column padded to a consistent width.
func (t *Table) Render() {
	var widths []int
	for _, row := range t.rows {
		for i, c := range row {
			w := VisibleLen(c)
			switch {
			case i == len(widths):
				widths = append(widths, w)
			case w > widths[i]:
				widths[i] = w
			}
		}
	}
	for _, row := range t.rows {
		var b strings.Builder
		b.WriteString(t.indent)
		for i, c := range row {
			b.WriteString(c)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-VisibleLen(c)+t.gap))
			}
		}
		Println(strings.TrimRight(b.String(), " "))
	}
}
