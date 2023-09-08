// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToSnakeCase(t *testing.T) {
	assert.EqualValues(t, "some_test_var_at2d", toSnakeCase("SomeTestVarAt2d"))
}

func TestPrint(t *testing.T) {
	tData := &table{
		headers: []string{"A", "B"},
		values: [][]string{
			{"new a", "some bbbb"},
			{"AAAAA", "b2"},
		},
	}

	buf := &bytes.Buffer{}

	tData.fprint(buf, "json")
	result := []struct {
		A string
		B string
	}{}
	assert.NoError(t, json.NewDecoder(buf).Decode(&result))

	if assert.Len(t, result, 2) {
		assert.EqualValues(t, "new a", result[0].A)
	}
}
