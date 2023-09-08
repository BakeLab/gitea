// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
)

// MilestoneDetails print an milestone formatted to stdout
func MilestoneDetails(milestone *gitea.Milestone) {
	fmt.Printf("%s\n",
		milestone.Title,
	)
	if len(milestone.Description) != 0 {
		fmt.Printf("\n%s\n", milestone.Description)
	}
	if milestone.Deadline != nil && !milestone.Deadline.IsZero() {
		fmt.Printf("\nDeadline: %s\n", FormatTime(*milestone.Deadline, false))
	}
}

// MilestonesList prints a listing of milestones
func MilestonesList(news []*gitea.Milestone, output string, fields []string) {
	var printables = make([]printable, len(news))
	for i, x := range news {
		printables[i] = &printableMilestone{x}
	}
	t := tableFromItems(fields, printables, isMachineReadable(output))
	t.sort(0, true)
	t.print(output)
}

// MilestoneFields are all available fields to print with MilestonesList
var MilestoneFields = []string{
	"title",
	"state",
	"items_open",
	"items_closed",
	"items",
	"duedate",
	"description",
	"created",
	"updated",
	"closed",
	"id",
}

type printableMilestone struct {
	*gitea.Milestone
}

func (m printableMilestone) FormatField(field string, machineReadable bool) string {
	switch field {
	case "title":
		return m.Title
	case "state":
		return string(m.State)
	case "items_open":
		return fmt.Sprintf("%d", m.OpenIssues)
	case "items_closed":
		return fmt.Sprintf("%d", m.ClosedIssues)
	case "items":
		return fmt.Sprintf("%d/%d", m.OpenIssues, m.ClosedIssues)
	case "duedate":
		if m.Deadline != nil && !m.Deadline.IsZero() {
			return FormatTime(*m.Deadline, machineReadable)
		}
	case "id":
		return fmt.Sprintf("%d", m.ID)
	case "description":
		return m.Description
	case "created":
		return FormatTime(m.Created, machineReadable)
	case "updated":
		if m.Updated != nil {
			return FormatTime(*m.Updated, machineReadable)
		}
	case "closed":
		if m.Closed != nil {
			return FormatTime(*m.Closed, machineReadable)
		}
	}
	return ""
}
