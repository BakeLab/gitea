// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeWriter(t *testing.T) {
	const hostile = "+hello \x1b]0;PWNED\x07 \x1b[1;31mred\x1b[0m\x1b[2J\r\n\u009b2J\u202eend\t\xff\x1b[1111111111111111111111111111111111m\n"
	var piped bytes.Buffer
	_, err := SafeWriter(&piped, false, false).Write([]byte(hostile))
	require.NoError(t, err)
	require.Equal(t, hostile, piped.String())
	for _, tc := range []struct {
		name     string
		allowSGR bool
		want     string
	}{
		{"drops every escape", false, "+hello ]0;PWNED [1;31mred[0m[2J\n2Jend\t\uFFFD[1111111111111111111111111111111111m\n"},
		{"keeps colors", true, "+hello ]0;PWNED \x1b[1;31mred\x1b[0m[2J\n2Jend\t\uFFFD[1111111111111111111111111111111111m\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.allowSGR {
				require.Equal(t, SafeText(hostile), tc.want)
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
