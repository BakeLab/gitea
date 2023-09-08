// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
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

func (t *table) print(output string) {
	t.fprint(os.Stdout, output)
}

func (t *table) fprint(f io.Writer, output string) {
	switch output {
	case "", "table":
		outputTable(f, t.headers, t.values)
	case "csv":
		outputDsv(f, t.headers, t.values, ",")
	case "simple":
		outputSimple(f, t.headers, t.values)
	case "tsv":
		outputDsv(f, t.headers, t.values, "\t")
	case "yml", "yaml":
		outputYaml(f, t.headers, t.values)
	case "json":
		outputJSON(f, t.headers, t.values)
	default:
		fmt.Fprintf(f, `"unknown output type '%s', available types are:
- csv: comma-separated values
- simple: space-separated values
- table: auto-aligned table format (default)
- tsv: tab-separated values
- yaml: YAML format
- json: JSON format
`, output)
		os.Exit(1)
	}
}

// outputTable prints structured data as table
func outputTable(f io.Writer, headers []string, values [][]string) {
	table := tablewriter.NewWriter(f)
	if len(headers) > 0 {
		table.SetHeader(headers)
	}
	for _, value := range values {
		table.Append(value)
	}
	table.Render()
}

// outputSimple prints structured data as space delimited value
func outputSimple(f io.Writer, headers []string, values [][]string) {
	for _, value := range values {
		fmt.Fprint(f, strings.Join(value, " "))
		fmt.Fprintf(f, "\n")
	}
}

// outputDsv prints structured data as delimiter separated value format
func outputDsv(f io.Writer, headers []string, values [][]string, delimiterOpt ...string) {
	delimiter := ","
	if len(delimiterOpt) > 0 {
		delimiter = delimiterOpt[0]
	}
	fmt.Fprintln(f, "\""+strings.Join(headers, "\""+delimiter+"\"")+"\"")
	for _, value := range values {
		fmt.Fprintf(f, "\"")
		fmt.Fprint(f, strings.Join(value, "\""+delimiter+"\""))
		fmt.Fprintf(f, "\"")
		fmt.Fprintf(f, "\n")
	}
}

// outputYaml prints structured data as yaml
func outputYaml(f io.Writer, headers []string, values [][]string) {
	for _, value := range values {
		fmt.Fprintln(f, "-")
		for j, val := range value {
			intVal, _ := strconv.Atoi(val)
			if strconv.Itoa(intVal) == val {
				fmt.Fprintf(f, "    %s: %s\n", headers[j], val)
			} else {
				fmt.Fprintf(f, "    %s: '%s'\n", headers[j], val)
			}
		}
	}
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

// outputJSON prints structured data as json
func outputJSON(f io.Writer, headers []string, values [][]string) {
	fmt.Fprintln(f, "[")
	itemCount := len(values)
	headersCount := len(headers)
	const space = "  "
	for i, value := range values {
		fmt.Fprintf(f, "%s{\n", space)
		for j, val := range value {
			intVal, _ := strconv.Atoi(val)
			if strconv.Itoa(intVal) == val {
				fmt.Fprintf(f, "%s%s\"%s\": %s", space, space, toSnakeCase(headers[j]), val)
			} else {
				fmt.Fprintf(f, "%s%s\"%s\": \"%s\"", space, space, toSnakeCase(headers[j]), val)
			}
			if j != headersCount-1 {
				fmt.Fprintln(f, ",")
			} else {
				fmt.Fprintln(f)
			}
		}

		if i != itemCount-1 {
			fmt.Fprintf(f, "%s},\n", space)
		} else {
			fmt.Fprintf(f, "%s}\n", space)
		}
	}
	fmt.Fprintln(f, "]")
}

func isMachineReadable(outputFormat string) bool {
	switch outputFormat {
	case "yml", "yaml", "csv", "tsv", "json":
		return true
	}
	return false
}
