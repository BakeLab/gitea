// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"io"
	"unicode"
	"unicode/utf8"
)

// maxSGRLength bounds how many bytes of a possible SGR sequence are held back between writes.
const maxSGRLength = 256

// SafeWriter returns w unchanged when tty is false, so piped output stays byte-exact.
// On a terminal it filters streamed server text like SafeText, and with allowSGR it keeps
// SGR color sequences (ESC [ ... m) while dropping every other escape sequence's ESC; ResetSGR
// then ends the colors it passed.
func SafeWriter(w io.Writer, tty, allowSGR bool) io.Writer {
	if !tty {
		return w
	}
	return &safeWriter{w: w, allowSGR: allowSGR}
}

// SafeColorWriter is SafeWriter for text that is colored throughout, such as job logs: without
// color it drops SGR sequences whole instead of showing them as "[36m" text.
func SafeColorWriter(w io.Writer, tty, color bool) io.Writer {
	if !tty {
		return w
	}
	return &safeWriter{w: w, allowSGR: color, dropSGR: !color}
}

// ResetSGR resets the terminal's colors when the SafeWriter w passed SGR sequences, so that text
// cut off while colored, such as a failed job's log, does not leave the terminal colored.
func ResetSGR(w io.Writer) error {
	s, ok := w.(*safeWriter)
	if !ok || !s.colored {
		return nil
	}
	s.colored = false
	_, err := io.WriteString(s.w, "\x1b[0m")
	return err
}

type safeWriter struct {
	w        io.Writer
	allowSGR bool
	// dropSGR drops SGR sequences instead of passing them.
	dropSGR bool
	// colored is set once an SGR sequence was passed.
	colored bool
	// pending holds a rune or SGR sequence split across writes.
	pending []byte
}

func (s *safeWriter) Write(p []byte) (int, error) {
	data := s.pending
	data = append(data, p...)
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if (s.allowSGR || s.dropSGR) && data[i] == '\x1b' {
			n, complete := sgrLength(data[i:])
			if !complete {
				break
			}
			if n == 0 {
				i++
				continue
			}
			if s.allowSGR {
				out = append(out, data[i:i+n]...)
				s.colored = true
			}
			i += n
			continue
		}
		if !utf8.FullRune(data[i:]) {
			break
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == '\t' || r == '\n' || !(unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			out = utf8.AppendRune(out, r)
		}
		i += size
	}
	s.pending = append(s.pending[:0], data[i:]...)
	if _, err := s.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// sgrLength returns the length of the SGR sequence at the start of b, which begins with ESC,
// or 0 when b starts another escape sequence. complete is false while b may be a truncated SGR sequence.
func sgrLength(b []byte) (n int, complete bool) {
	if len(b) < 2 {
		return 0, false
	}
	if b[1] != '[' {
		return 0, true
	}
	for j := 2; j < len(b); j++ {
		c := b[j]
		if c == 'm' {
			return j + 1, true
		}
		if j >= maxSGRLength || ((c < '0' || c > '9') && c != ';' && c != ':') {
			return 0, true
		}
	}
	return 0, false
}
