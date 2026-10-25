package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// useColor is true when stdout is a terminal that can show ANSI colors and
// Unicode box characters. It is false when piped, or when NO_COLOR is set.
var useColor = os.Getenv("NO_COLOR") == "" && enableVT()

func paint(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string   { return paint("1", s) }
func dim(s string) string    { return paint("2", s) }
func red(s string) string    { return paint("31", s) }
func green(s string) string  { return paint("32", s) }
func yellow(s string) string { return paint("33", s) }
func cyan(s string) string   { return paint("36", s) }

// termWidth is the terminal's column count, falling back to $COLUMNS, then 80.
func termWidth() int {
	if w := consoleWidth(); w > 0 {
		return w
	}
	if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && w > 0 {
		return w
	}
	return 80
}

// box prints lines inside a frame under a title, so program output stands
// apart from menu choices. The frame is drawn in color. It is as wide as the
// widest line, and lines longer than the terminal are wrapped inside it.
func box(title string, color func(string) string, lines []string) {
	tl, tr, bl, br, h, v := "+", "+", "+", "+", "-", "|"
	if useColor {
		tl, tr, bl, br, h, v = "╭", "╮", "╰", "╯", "─", "│"
	}

	max := termWidth() - 4 // two border columns and two spaces of padding
	if max < 20 {
		max = 20
	}
	inner := visibleLen(title) + 1
	for _, l := range lines {
		if n := visibleLen(l); n > inner {
			inner = n
		}
	}
	if inner > max {
		inner = max
	}

	var wrapped []string
	for _, l := range lines {
		wrapped = append(wrapped, wrap(l, inner)...)
	}

	fmt.Println(color(tl+h+" ") + bold(title) + " " + color(strings.Repeat(h, inner-visibleLen(title)-1)+tr))
	for _, l := range wrapped {
		fmt.Println(color(v) + " " + l + strings.Repeat(" ", inner-visibleLen(l)) + " " + color(v))
	}
	fmt.Println(color(bl + strings.Repeat(h, inner+2) + br))
}

// visibleLen is the number of characters s shows, ignoring ANSI escapes.
func visibleLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if end := escapeEnd(s, i); end > i {
			i = end
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// escapeEnd returns the index just past an ANSI "ESC [ ... m" sequence starting
// at s[i], or i if there is none.
func escapeEnd(s string, i int) int {
	if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
		return i
	}
	for j := i + 2; j < len(s); j++ {
		if s[j] == 'm' {
			return j + 1
		}
	}
	return i
}

// wrap splits s into pieces of at most width visible characters, closing and
// reopening any color across the breaks.
func wrap(s string, width int) []string {
	if visibleLen(s) <= width {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	active, n := "", 0
	flush := func() {
		if active != "" {
			cur.WriteString("\x1b[0m")
		}
		out = append(out, cur.String())
		cur.Reset()
		cur.WriteString(active)
		n = 0
	}
	for i := 0; i < len(s); {
		if end := escapeEnd(s, i); end > i {
			seq := s[i:end]
			cur.WriteString(seq)
			if seq == "\x1b[0m" {
				active = ""
			} else {
				active += seq
			}
			i = end
			continue
		}
		if n == width {
			flush()
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		cur.WriteString(s[i : i+size])
		i += size
		n++
	}
	out = append(out, cur.String())
	return out
}
