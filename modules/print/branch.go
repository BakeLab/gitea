// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
)

// BranchesList prints a listing of the branches
func BranchesList(branches []*gitea.Branch, protections []*gitea.BranchProtection, output string, fields []string) {
	fmt.Println(fields)
	printables := make([]printable, len(branches))

	for i, branch := range branches {
		var protection *gitea.BranchProtection
		for _, p := range protections {
			if p.BranchName == branch.Name {
				protection = p
			}
		}
		printables[i] = &printableBranch{branch, protection}
	}

	t := tableFromItems(fields, printables, isMachineReadable(output))
	t.print(output)
}

type printableBranch struct {
	branch     *gitea.Branch
	protection *gitea.BranchProtection
}

func (x printableBranch) FormatField(field string, machineReadable bool) string {
	switch field {
	case "name":
		return x.branch.Name
	case "protected":
		return fmt.Sprintf("%t", x.branch.Protected)
	case "user-can-merge":
		return fmt.Sprintf("%t", x.branch.UserCanMerge)
	case "user-can-push":
		return fmt.Sprintf("%t", x.branch.UserCanPush)
	case "protection":
		if x.protection != nil {
			approving := ""
			for _, entry := range x.protection.ApprovalsWhitelistTeams {
				approving += entry + "/"
			}
			for _, entry := range x.protection.ApprovalsWhitelistUsernames {
				approving += entry + "/"
			}
			merging := ""
			for _, entry := range x.protection.MergeWhitelistTeams {
				approving += entry + "/"
			}
			for _, entry := range x.protection.MergeWhitelistUsernames {
				approving += entry + "/"
			}
			pushing := ""
			for _, entry := range x.protection.PushWhitelistTeams {
				approving += entry + "/"
			}
			for _, entry := range x.protection.PushWhitelistUsernames {
				approving += entry + "/"
			}
			return fmt.Sprintf(
				"- enable-push: %t\n- approving: %s\n- merging: %s\n- pushing: %s\n",
				x.protection.EnablePush, approving, merging, pushing,
			)
		}
		return "<None>"
	}
	return ""
}

// BranchFields are all available fields to print with BranchesList()
var BranchFields = []string{
	"name",
	"protected",
	"user-can-merge",
	"user-can-push",
	"protection",
}
