// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package flags

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestPaginationFlags(t *testing.T) {
	var (
		defaultPage  = PaginationPageFlag.Value
		defaultLimit = PaginationLimitFlag.Value
	)

	cases := []struct {
		name          string
		args          []string
		expectedPage  int
		expectedLimit int
	}{
		{
			name:          "no flags",
			args:          []string{"test"},
			expectedPage:  defaultPage,
			expectedLimit: defaultLimit,
		},
		{
			name:          "only paging",
			args:          []string{"test", "--page", "5"},
			expectedPage:  5,
			expectedLimit: defaultLimit,
		},
		{
			name:          "only limit",
			args:          []string{"test", "--limit", "10"},
			expectedPage:  defaultPage,
			expectedLimit: 10,
		},
		{
			name:          "only limit",
			args:          []string{"test", "--limit", "10"},
			expectedPage:  defaultPage,
			expectedLimit: 10,
		},
		{
			name:          "both flags",
			args:          []string{"test", "--page", "2", "--limit", "20"},
			expectedPage:  2,
			expectedLimit: 20,
		},
		{ //TODO: Should no paging be applied as -1 or a separate flag? It's not obvious that page=-1 turns off paging and limit is ignored
			name:          "no paging",
			args:          []string{"test", "--limit", "20", "--page", "-1"},
			expectedPage:  -1,
			expectedLimit: 20,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cli.Command{
				Name: "test-paging",
				Action: func(_ context.Context, cmd *cli.Command) error {
					assert.Equal(t, tc.expectedPage, cmd.Int("page"))
					assert.Equal(t, tc.expectedLimit, cmd.Int("limit"))
					return nil
				},
				Flags: PaginationFlags,
			}
			err := cmd.Run(context.Background(), tc.args)
			require.NoError(t, err)
		})
	}

}
func TestPaginationFailures(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		expectedError error
	}{
		{
			name:          "negative limit",
			args:          []string{"test", "--limit", "-10"},
			expectedError: ErrLimit,
		},
		{
			name:          "negative paging",
			args:          []string{"test", "--page", "-2"},
			expectedError: ErrPage,
		},
		{
			name:          "zero paging",
			args:          []string{"test", "--page", "0"},
			expectedError: ErrPage,
		},
		{
			//urfave does not validate all flags in one pass
			name:          "negative paging and paging",
			args:          []string{"test", "--page", "-2", "--limit", "-10"},
			expectedError: ErrPage,
		},
	}

	for _, tc := range cases {
		cmd := cli.Command{
			Name:      "test-paging",
			Flags:     PaginationFlags,
			Writer:    io.Discard,
			ErrWriter: io.Discard,
		}
		t.Run(tc.name, func(t *testing.T) {
			err := cmd.Run(context.Background(), tc.args)
			require.ErrorContains(t, err, tc.expectedError.Error())
			// require.ErrorIs(t, err, tc.expectedError)
		})
	}
}
