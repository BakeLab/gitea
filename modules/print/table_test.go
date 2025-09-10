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
			{"\"abc", "\"def"},
			{"'abc", "de'f"},
			{"\\abc", "'def\\"},
		},
	}

	buf := &bytes.Buffer{}

	tData.fprint(buf, "json")
	result := []struct {
		A string
		B string
	}{}
	assert.NoError(t, json.NewDecoder(buf).Decode(&result))

	if assert.Len(t, result, 5) {
		assert.EqualValues(t, "new a", result[0].A)
		assert.EqualValues(t, "some bbbb", result[0].B)
		assert.EqualValues(t, "AAAAA", result[1].A)
		assert.EqualValues(t, "b2", result[1].B)
		assert.EqualValues(t, "\"abc", result[2].A)
		assert.EqualValues(t, "\"def", result[2].B)
		assert.EqualValues(t, "'abc", result[3].A)
		assert.EqualValues(t, "de'f", result[3].B)
		assert.EqualValues(t, "\\abc", result[4].A)
		assert.EqualValues(t, "'def\\", result[4].B)
	}
}
