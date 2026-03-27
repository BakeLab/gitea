// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"bytes"
	"encoding/json"
	"testing"

	"code.gitea.io/sdk/gitea"
	"github.com/stretchr/testify/require"
)

func TestReposListUsesNumericIDField(t *testing.T) {
	repos := []*gitea.Repository{{
		ID:   123,
		Name: "tea",
		Owner: &gitea.User{
			UserName: "gitea",
		},
	}}

	buf := &bytes.Buffer{}
	tbl := tableFromItems([]string{"id", "name"}, []printable{&printableRepo{repos[0]}}, true)
	require.NoError(t, tbl.fprint(buf, "json"))

	var result []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	require.Equal(t, "123", result[0]["id"])
	require.Equal(t, "tea", result[0]["name"])
}
