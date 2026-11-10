// Package markdown renders the small Markdown subset allowed in reviews and replies to HTML
// (docs/spec/reviews.md section 4). Every character of the input is HTML-escaped; the only tags
// in the output are the ones this package writes itself, and none of them carries an attribute.
//
// Blocks, separated by blank lines:
//
//	paragraph        consecutive lines, joined with <br>
//	> quote          consecutive lines starting with ">"
//	- item           consecutive lines starting with "- " or "* "
//
// Inline: `code`, **bold**, *italic*. There are no links, images, headings or raw HTML.
package markdown

import (
	"html"
	"strings"
)

// HTML renders src.
func HTML(src string) string {
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	var (
		b     strings.Builder
		kind  byte // 0 none, 'p', 'q', 'l'
		lines []string
	)
	flush := func() {
		switch kind {
		case 'p':
			b.WriteString("<p>")
			writeLines(&b, lines)
			b.WriteString("</p>")
		case 'q':
			b.WriteString("<blockquote><p>")
			writeLines(&b, lines)
			b.WriteString("</p></blockquote>")
		case 'l':
			b.WriteString("<ul>")
			for _, l := range lines {
				b.WriteString("<li>")
				b.WriteString(Inline(l))
				b.WriteString("</li>")
			}
			b.WriteString("</ul>")
		}
		kind, lines = 0, lines[:0]
	}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, " \t")
		trimmed := strings.TrimLeft(line, " \t")
		var (
			k    byte
			text string
		)
		switch {
		case trimmed == "":
			flush()
			continue
		case strings.HasPrefix(trimmed, ">"):
			k, text = 'q', strings.TrimLeft(trimmed[1:], " \t")
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			k, text = 'l', strings.TrimLeft(trimmed[2:], " \t")
		default:
			k, text = 'p', trimmed
		}
		if k != kind {
			flush()
			kind = k
		}
		lines = append(lines, text)
	}
	flush()
	return b.String()
}

func writeLines(b *strings.Builder, lines []string) {
	for i, l := range lines {
		if i > 0 {
			b.WriteString("<br>")
		}
		b.WriteString(Inline(l))
	}
}

// Inline renders the inline markup of one line.
func Inline(s string) string {
	var b strings.Builder
	inline(&b, s, true)
	return b.String()
}

// inline writes s. A marker without a matching closing marker is literal text. Emphasis must
// hug its text (no space after the opening or before the closing marker) and cannot be empty.
func inline(b *strings.Builder, s string, emphasis bool) {
	lit := 0 // start of pending literal text
	emit := func(end int) {
		b.WriteString(html.EscapeString(s[lit:end]))
	}
	for i := 0; i < len(s); {
		switch {
		case s[i] == '`':
			if end := strings.IndexByte(s[i+1:], '`'); end > 0 {
				emit(i)
				b.WriteString("<code>")
				b.WriteString(html.EscapeString(s[i+1 : i+1+end]))
				b.WriteString("</code>")
				i += end + 2
				lit = i
				continue
			}
		case emphasis && strings.HasPrefix(s[i:], "**"):
			if end := closing(s[i+2:], "**"); end > 0 {
				emit(i)
				b.WriteString("<strong>")
				inline(b, s[i+2:i+2+end], true)
				b.WriteString("</strong>")
				i += end + 4
				lit = i
				continue
			}
			i += 2 // literal "**": do not read its second star as an italic marker
			continue
		case emphasis && s[i] == '*':
			if end := closing(s[i+1:], "*"); end > 0 {
				emit(i)
				b.WriteString("<em>")
				inline(b, s[i+1:i+1+end], false)
				b.WriteString("</em>")
				i += end + 2
				lit = i
				continue
			}
		}
		i++
	}
	emit(len(s))
}

// closing returns the index in s of the marker that closes an emphasis starting at s[0], or -1.
func closing(s, marker string) int {
	if s == "" || s[0] == ' ' || s[0] == '\t' {
		return -1
	}
	from := 0
	for {
		i := strings.Index(s[from:], marker)
		if i < 0 {
			return -1
		}
		i += from
		if i > 0 && s[i-1] != ' ' && s[i-1] != '\t' {
			// A single star must not be the first half of "**".
			if marker == "*" && strings.HasPrefix(s[i:], "**") {
				from = i + 2
				continue
			}
			return i
		}
		from = i + len(marker)
	}
}
