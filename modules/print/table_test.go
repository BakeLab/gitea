// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

	require.NoError(t, tData.fprint(buf, "json"))
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

	buf.Reset()

	require.NoError(t, tData.fprint(buf, "yaml"))

	var yamlResult []map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &yamlResult))
	assert.Equal(t, []map[string]string{
		{"A": "new a", "B": "some bbbb"},
		{"A": "AAAAA", "B": "b2"},
		{"A": "\"abc", "B": "\"def"},
		{"A": "'abc", "B": "de'f"},
		{"A": "\\abc", "B": "'def\\"},
	}, yamlResult)
}

func TestPrintCSVUsesEscaping(t *testing.T) {
	tData := &table{
		headers: []string{"A", "B"},
		values: [][]string{
			{"hello,world", `quote "here"`},
			{"multi\nline", "plain"},
		},
	}

	buf := &bytes.Buffer{}
	require.NoError(t, tData.fprint(buf, "csv"))

	reader := csv.NewReader(bytes.NewReader(buf.Bytes()))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Equal(t, [][]string{
		{"A", "B"},
		{"hello,world", `quote "here"`},
		{"multi\nline", "plain"},
	}, records)
}

func TestPrintJSONPreservesFieldOrder(t *testing.T) {
	tData := &table{
		headers: []string{"Zebra", "Apple", "Mango"},
		values:  [][]string{{"z", "a", "m"}},
	}

	buf := &bytes.Buffer{}
	require.NoError(t, tData.fprint(buf, "json"))

	// Keys must appear in header order (Zebra, Apple, Mango), not sorted alphabetically
	raw := buf.String()
	zebraIdx := bytes.Index([]byte(raw), []byte(`"zebra"`))
	appleIdx := bytes.Index([]byte(raw), []byte(`"apple"`))
	mangoIdx := bytes.Index([]byte(raw), []byte(`"mango"`))
	assert.Greater(t, appleIdx, zebraIdx, "apple should appear after zebra")
	assert.Greater(t, mangoIdx, appleIdx, "mango should appear after apple")
}

func TestPrintUnknownOutputReturnsError(t *testing.T) {
	tData := &table{headers: []string{"A"}, values: [][]string{{"value"}}}

	err := tData.fprint(io.Discard, "unknown")
	require.ErrorContains(t, err, `unknown output type "unknown"`)
}
