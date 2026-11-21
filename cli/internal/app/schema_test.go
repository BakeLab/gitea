// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

const schemaGolden = "testdata/schema.json.txt"

func runSchema(t *testing.T, args ...string) schemaReport {
	t.Helper()
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, append([]string{"schema"}, args...)))
	var report schemaReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	return report
}

func describeSchema(c commandSchema) []string {
	fields := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		fields[i] = f.Name + ":" + f.Type
		if f.Format != "" {
			fields[i] += "(" + f.Format + ")"
		}
		if f.Items != "" {
			fields[i] += "<" + f.Items + ">"
		}
		if f.Nullable {
			fields[i] += "?"
		}
	}
	return fields
}

func TestSchemaCommand(t *testing.T) {
	report := runSchema(t, "issue", "list")
	require.Equal(t, schemaVersion, report.Version)
	require.Len(t, report.Commands, 1)
	require.Equal(t, "gt issue list", report.Commands[0].Command)
	described := describeSchema(report.Commands[0])
	require.Contains(t, described, "number:integer")
	require.Contains(t, described, "title:string")
	require.Contains(t, described, "labels:array<object>?")

	require.Len(t, runSchema(t, "pr", "list").Commands, 1)

	all := runSchema(t)
	require.Greater(t, len(all.Commands), 50)
	for _, c := range all.Commands {
		command := mustFind(t, strings.Fields(c.Command)[1:])
		require.True(t, outputsJSON(command), c.Command)
		require.Len(t, c.Fields, len(commandOutputFields(command)), c.Command)
		if fieldsDeclaration(command).Annotations[outputTypeAnnotation] != "" { // varyingJSONOutput has no types
			for _, field := range c.Fields {
				require.NotEmpty(t, field.Type, "%s: the field %s has no type", c.Command, field.Name)
			}
		}
	}

	for _, args := range [][]string{{"bogus"}, {"issue", "list", "extra"}, {"version"}, {"branch", "delete"}} {
		deps, out, _ := testDeps(t)
		err := Execute(context.Background(), deps, append([]string{"schema"}, args...))
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v: %v", args, err)
		require.Empty(t, out.String())
	}
}

func mustFind(t *testing.T, path []string) (command *cobra.Command) {
	t.Helper()
	deps, _, _ := testDeps(t)
	command, _, err := NewRoot(deps).Find(path)
	require.NoError(t, err)
	return command
}

// TestSchemaIsBackwardCompatible enforces the promise of gt help formatting: a command's JSON
// fields are never removed and keep their type. A new field or command passes; to accept an
// intended break or to record additions, run UPDATE_SCHEMA=1 go test -run TestSchemaIsBackwardCompatible ./internal/app/.
func TestSchemaIsBackwardCompatible(t *testing.T) {
	current := map[string][]string{}
	var commands []string
	for _, c := range runSchema(t).Commands {
		current[c.Command] = describeSchema(c)
		commands = append(commands, c.Command)
	}
	slices.Sort(commands)
	var golden strings.Builder
	for _, name := range commands {
		golden.WriteString(name + "\t" + strings.Join(current[name], ",") + "\n")
	}
	if os.Getenv("UPDATE_SCHEMA") != "" {
		require.NoError(t, os.WriteFile(schemaGolden, []byte(golden.String()), 0o644))
	}
	data, err := os.ReadFile(schemaGolden)
	require.NoError(t, err)
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		name, fields, _ := strings.Cut(line, "\t")
		now, ok := current[name]
		require.True(t, ok, "%s no longer outputs JSON", name)
		for field := range strings.SplitSeq(fields, ",") {
			require.Contains(t, now, field, "%s lost or changed the JSON field %s", name, field)
		}
	}
}
