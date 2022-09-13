// Copyright 2020 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package print

import (
	"fmt"
	"strings"

	"code.gitea.io/sdk/gitea"
)

// NotificationsList prints a listing of notification threads
func NotificationsList(news []*gitea.NotificationThread, output string, fields []string) {
	var printables = make([]printable, len(news))
	for i, x := range news {
		printables[i] = &printableNotification{x}
	}
	t := tableFromItems(fields, printables, isMachineReadable(output))
	t.print(output)
}

// NotificationFields are all available fields to print with NotificationsList
var NotificationFields = []string{
	"id",
	"status",
	"updated",

	// these are about the notification subject
	"index",
	"type",
	"state",
	"title",
	"repository",
}

type printableNotification struct {
	*gitea.NotificationThread
}

func (n printableNotification) FormatField(field string, machineReadable bool) string {
	switch field {
	case "id":
		return fmt.Sprintf("%d", n.ID)

	case "status":
		status := "read"
		if n.Pinned {
			status = "pinned"
		} else if n.Unread {
			status = "unread"
		}
		return status

	case "updated":
		return FormatTime(n.UpdatedAt, machineReadable)

	case "index":
		var index string
		if n.Subject.Type == "Issue" || n.Subject.Type == "Pull" {
			index = n.Subject.URL
			urlParts := strings.Split(n.Subject.URL, "/")
			if len(urlParts) != 0 {
				index = urlParts[len(urlParts)-1]
			}
		}
		return index

	case "type":
		return string(n.Subject.Type)

	case "state":
		return string(n.Subject.State)

	case "title":
		return n.Subject.Title

	case "repo", "repository":
		return n.Repository.FullName
	}
	return ""
}
