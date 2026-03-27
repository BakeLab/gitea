// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v3"
)

// table provides infrastructure to easily print (sorted) lists in different formats
type table struct {
	headers    []string
	values     [][]string
	sortDesc   bool // used internally by sortable interface
	sortColumn uint // ↑
}

// printable can be implemented for structs to put fields dynamically into a table
type printable interface {
	FormatField(field string, machineReadable bool) string
}

// high level api to print a table of items with dynamic fields
func tableFromItems(fields []string, values []printable, machineReadable bool) table {
	t := table{headers: fields}
	for _, v := range values {
		row := make([]string, len(fields))
		for i, f := range fields {
			row[i] = v.FormatField(f, machineReadable)
		}
		t.addRowSlice(row)
	}
	return t
}

func tableWithHeader(header ...string) table {
	return table{headers: header}
}

// it's the callers responsibility to ensure row length is equal to header length!
func (t *table) addRow(row ...string) {
	t.addRowSlice(row)
}

// it's the callers responsibility to ensure row length is equal to header length!
func (t *table) addRowSlice(row []string) {
	t.values = append(t.values, row)
}

func (t *table) sort(column uint, desc bool) {
	t.sortColumn = column
	t.sortDesc = desc
	sort.Stable(t) // stable to allow multiple calls to sort
}

// sortable interface
func (t table) Len() int      { return len(t.values) }
func (t table) Swap(i, j int) { t.values[i], t.values[j] = t.values[j], t.values[i] }
func (t table) Less(i, j int) bool {
	if t.sortDesc {
		i, j = j, i
	}
	return t.values[i][t.sortColumn] < t.values[j][t.sortColumn]
}

func (t *table) print(output string) error {
	return t.fprint(os.Stdout, output)
}

func (t *table) fprint(f io.Writer, output string) error {
	switch output {
	case "", "table":
		return outputTable(f, t.headers, t.values)
	case "csv":
		return outputDsv(f, t.headers, t.values, ',')
	case "simple":
		return outputSimple(f, t.headers, t.values)
	case "tsv":
		return outputDsv(f, t.headers, t.values, '\t')
	case "yml", "yaml":
		return outputYaml(f, t.headers, t.values)
	case "json":
		return outputJSON(f, t.headers, t.values)
	default:
		return fmt.Errorf("unknown output type %q, available types are: csv, simple, table, tsv, yaml, json", output)
	}
}

// outputTable prints structured data as table
func outputTable(f io.Writer, headers []string, values [][]string) error {
	table := tablewriter.NewWriter(f)
	if len(headers) > 0 {
		table.Header(headers)
	}
	for _, value := range values {
		if err := table.Append(value); err != nil {
			return err
		}
	}
	return table.Render()
}

// outputSimple prints structured data as space delimited value
func outputSimple(f io.Writer, headers []string, values [][]string) error {
	for _, value := range values {
		if _, err := fmt.Fprintln(f, strings.Join(value, " ")); err != nil {
			return err
		}
	}
	return nil
}

// outputDsv prints structured data as delimiter separated value format.
func outputDsv(f io.Writer, headers []string, values [][]string, delimiter rune) error {
	writer := csv.NewWriter(f)
	writer.Comma = delimiter
	if err := writer.Write(headers); err != nil {
		return err
	}
	for _, value := range values {
		if err := writer.Write(value); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// outputYaml prints structured data as yaml
func outputYaml(f io.Writer, headers []string, values [][]string) error {
	root := &yaml.Node{Kind: yaml.SequenceNode}
	for _, value := range values {
		row := &yaml.Node{Kind: yaml.MappingNode}
		for j, val := range value {
			row.Content = append(row.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: headers[j],
			})

			valueNode := &yaml.Node{Kind: yaml.ScalarNode, Value: val}
			intVal, _ := strconv.Atoi(val)
			if strconv.Itoa(intVal) == val {
				valueNode.Tag = "!!int"
			} else {
				valueNode.Tag = "!!str"
			}
			row.Content = append(row.Content, valueNode)
		}
		root.Content = append(root.Content, row)
	}
	encoder := yaml.NewEncoder(f)
	if err := encoder.Encode(root); err != nil {
		_ = encoder.Close()
		return err
	}
	return encoder.Close()
}

var (
	matchFirstCap = regexp.MustCompile("(.)([A-Z][a-z]+)")
	matchAllCap   = regexp.MustCompile("([a-z0-9])([A-Z])")
)

func toSnakeCase(str string) string {
	snake := matchFirstCap.ReplaceAllString(str, "${1}_${2}")
	snake = matchAllCap.ReplaceAllString(snake, "${1}_${2}")
	return strings.ToLower(snake)
}

// orderedRow preserves header insertion order when marshaled to JSON.
type orderedRow struct {
	keys   []string
	values map[string]string
}

func (o orderedRow) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		val, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// outputJSON prints structured data as json, preserving header field order.
func outputJSON(f io.Writer, headers []string, values [][]string) error {
	snakeHeaders := make([]string, len(headers))
	for i, h := range headers {
		snakeHeaders[i] = toSnakeCase(h)
	}
	rows := make([]orderedRow, 0, len(values))
	for _, value := range values {
		row := orderedRow{keys: snakeHeaders, values: make(map[string]string, len(headers))}
		for j, val := range value {
			row.values[snakeHeaders[j]] = val
		}
		rows = append(rows, row)
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(rows)
}

func isMachineReadable(outputFormat string) bool {
	switch outputFormat {
	case "yml", "yaml", "csv", "tsv", "json":
		return true
	}
	return false
}
