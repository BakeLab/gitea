// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeWriterKeepsLongSGRSequences(t *testing.T) {
	const colored = "\x1b[38;2;255;255;255;48;2;255;255;255mwhite\x1b[0m\n"
	for _, chunk := range []int{len(colored), 1, 3} {
		var out bytes.Buffer
		w := SafeWriter(&out, true, true)
		for data := []byte(colored); len(data) > 0; {
			n := min(chunk, len(data))
			_, err := w.Write(data[:n])
			require.NoError(t, err)
			data = data[n:]
		}
		require.Equal(t, colored, out.String(), "chunk size %d", chunk)
	}
}

func TestResetSGREndsPassedColors(t *testing.T) {
	for _, tc := range []struct {
		name          string
		tty, allowSGR bool
		want          string
	}{
		{"piped", false, true, "\x1b[31mred"},
		{"colors dropped", true, false, "[31mred"},
		{"colors kept", true, true, "\x1b[31mred\x1b[0m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			w := SafeWriter(&out, tc.tty, tc.allowSGR)
			_, err := w.Write([]byte("\x1b[31mred"))
			require.NoError(t, err)
			require.NoError(t, ResetSGR(w))
			// Only colors passed since the last reset are reset.
			require.NoError(t, ResetSGR(w))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestSafeWriter(t *testing.T) {
	long := strings.Repeat("1", 300)
	hostile := "+hello \x1b]0;PWNED\x07 \x1b[1;31mred\x1b[0m\x1b[2J\r\n\u009b2J\u202eend\t\xff\x1b[" + long + "m\n"
	var piped bytes.Buffer
	_, err := SafeWriter(&piped, false, false).Write([]byte(hostile))
	require.NoError(t, err)
	require.Equal(t, hostile, piped.String())
	for _, tc := range []struct {
		name     string
		allowSGR bool
		want     string
	}{
		{"drops every escape", false, "+hello ]0;PWNED [1;31mred[0m[2J\n2Jend\t\uFFFD[" + long + "m\n"},
		{"keeps colors", true, "+hello ]0;PWNED \x1b[1;31mred\x1b[0m[2J\n2Jend\t\uFFFD[" + long + "m\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.allowSGR {
				require.Equal(t, tc.want, SafeText(hostile))
			}
			for _, chunk := range []int{len(hostile), 1, 3} {
				var out bytes.Buffer
				w := SafeWriter(&out, true, tc.allowSGR)
				for data := []byte(hostile); len(data) > 0; {
					n := min(chunk, len(data))
					written, err := w.Write(data[:n])
					require.NoError(t, err)
					require.Equal(t, n, written)
					data = data[n:]
				}
				require.Equal(t, tc.want, out.String(), "chunk size %d", chunk)
			}
		})
	}
}

func TestSafeColorWriterDropsColorsWholeWithoutColor(t *testing.T) {
	const log = "\x1b[36mstep\x1b[0m \x1b]0;PWNED\x07done\x1b[2J\n"
	var piped bytes.Buffer
	_, err := SafeColorWriter(&piped, false, false).Write([]byte(log))
	require.NoError(t, err)
	require.Equal(t, log, piped.String())
	for _, tc := range []struct {
		name  string
		color bool
		want  string
	}{
		{"colored", true, "\x1b[36mstep\x1b[0m ]0;PWNEDdone[2J\n\x1b[0m"},
		{"plain", false, "step ]0;PWNEDdone[2J\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, chunk := range []int{len(log), 1, 3} {
				var out bytes.Buffer
				w := SafeColorWriter(&out, true, tc.color)
				for data := []byte(log); len(data) > 0; {
					n := min(chunk, len(data))
					_, err := w.Write(data[:n])
					require.NoError(t, err)
					data = data[n:]
				}
				require.NoError(t, ResetSGR(w))
				require.Equal(t, tc.want, out.String(), "chunk size %d", chunk)
			}
		})
	}
}
