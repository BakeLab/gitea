// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"github.com/spf13/cobra"
)

// schemaVersion numbers the layout of gt schema's output.
const schemaVersion = 1

// outputTypeAnnotation names the type in outputTypes whose fields jsonOutput declared.
const outputTypeAnnotation = "gt.fields.type"

// fieldSchema describes one field that --json selects.
type fieldSchema struct {
	Name string `json:"name"`
	// Type is the JSON type: string, integer, number, boolean, array or object; empty when it varies.
	Type string `json:"type,omitempty"`
	// Items is the JSON type of an array's elements.
	Items string `json:"items,omitempty"`
	// Format is date-time for a timestamp string.
	Format string `json:"format,omitempty"`
	// Nullable is set when the field can be null or missing from the item.
	Nullable bool `json:"nullable,omitempty"`
}

// commandSchema lists the fields of the JSON that a command prints.
type commandSchema struct {
	Command string        `json:"command"`
	Fields  []fieldSchema `json:"fields"`
}

type schemaReport struct {
	Version  int             `json:"schema_version"`
	Commands []commandSchema `json:"commands"`
}

func (a *application) schemaCommand() *cobra.Command {
	return &cobra.Command{
		Use: "schema [<command>...]", Short: "Print the JSON fields that commands output",
		Long: `Print the fields that --json selects, with their JSON types, for every command that
outputs JSON, or for the commands under the one named. The output is JSON and can be filtered
with --jq. See gt help formatting for what scripts may rely on.`,
		Example: `$ gt schema issue list
$ gt schema --jq '.commands[] | select(.command == "gt pr view") | .fields[].name'`,
		Annotations: map[string]string{"gt.offline": "true", "gt.rawjson": "true", "gt.text": "schema always prints JSON"},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := NewRoot(Dependencies{}) // built-ins only, without aliases and extensions
			target := root
			if len(args) > 0 {
				found, rest, err := root.Find(args)
				if err != nil || len(rest) != 0 || found == root {
					return clierr.Wrap(clierr.Usage, errors.New("unknown command "+strings.Join(args, " ")+"; use gt help to list commands"))
				}
				target = found
			}
			report := schemaReport{Version: schemaVersion, Commands: []commandSchema{}}
			var walk func(*cobra.Command)
			walk = func(c *cobra.Command) {
				if fields := commandOutputFields(c); len(fields) > 0 && outputsJSON(c) {
					report.Commands = append(report.Commands, commandSchema{Command: c.CommandPath(), Fields: describeFields(c, fields)})
				}
				for _, child := range c.Commands() {
					walk(child)
				}
			}
			walk(target)
			if len(args) > 0 && len(report.Commands) == 0 {
				return clierr.Wrap(clierr.Usage, errors.New(target.CommandPath()+" outputs no JSON fields"))
			}
			slices.SortFunc(report.Commands, func(x, y commandSchema) int { return strings.Compare(x.Command, y.Command) })
			return a.writeJSON(cmd, report)
		},
	}
}

// describeFields pairs the field names cmd outputs with the types of its declaration, when known.
func describeFields(cmd *cobra.Command, names []string) []fieldSchema {
	known := map[string]fieldSchema{}
	if t, ok := outputTypes.Load(fieldsDeclaration(cmd).Annotations[outputTypeAnnotation]); ok {
		for _, field := range structFieldSchemas(t.(reflect.Type)) {
			known[field.Name] = field
		}
	}
	described := make([]fieldSchema, len(names))
	for i, name := range names {
		described[i] = cmp.Or(known[name], fieldSchema{Name: name})
	}
	return described
}

// structFieldSchemas describes the JSON fields of t in order, including those of embedded structs.
func structFieldSchemas(t reflect.Type) []fieldSchema {
	var fields []fieldSchema
	structJSONFields(t, func(name, options string, field reflect.Type) {
		described := fieldType(field)
		described.Name = name
		described.Nullable = described.Nullable || strings.Contains(","+options+",", ",omitempty,")
		fields = append(fields, described)
	})
	return fields
}

var timeType = reflect.TypeFor[time.Time]()

// fieldType maps a Go type to its JSON type.
func fieldType(t reflect.Type) fieldSchema {
	var described fieldSchema
	if t.Kind() == reflect.Pointer {
		described.Nullable, t = true, t.Elem()
	}
	switch {
	case t == timeType:
		described.Type, described.Format = "string", "date-time"
	case t.Kind() == reflect.String:
		described.Type = "string"
	case t.Kind() == reflect.Bool:
		described.Type = "boolean"
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Uintptr:
		described.Type = "integer"
	case t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64:
		described.Type = "number"
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		described.Type, described.Nullable = "array", described.Nullable || t.Kind() == reflect.Slice
		described.Items = fieldType(t.Elem()).Type
	case t.Kind() == reflect.Map || t.Kind() == reflect.Struct:
		described.Type = "object"
	}
	return described
}
