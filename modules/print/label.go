// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"strconv"
	"strings"

	gitea "gitea.dev/sdk"
)

// LabelsList prints a listing of labels
func LabelsList(labels []*gitea.Label, output string) error {
	t := tableWithHeader(
		"Index",
		"Color",
		"Name",
		"Description",
		"Level",
	)

	for _, label := range labels {
		level := "Repository"
		if strings.Contains(label.URL, "/orgs/") {
			level = "Organization"
		}
		t.addRow(
			strconv.FormatInt(label.ID, 10),
			formatLabel(label, !isMachineReadable(output), label.Color),
			label.Name,
			label.Description,
			level,
		)
	}
	return t.print(output)
}
