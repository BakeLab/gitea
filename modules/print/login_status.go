// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"
	"strings"
	"time"
)

// LoginStatus contains the authentication status of a single configured login.
type LoginStatus struct {
	Name        string
	URL         string
	User        string
	Valid       bool
	AuthMethod  string
	TokenExpiry time.Time
	Helper      bool
	Default     bool
	Error       string
}

// LoginStatusFields are the available fields to print with LoginStatuses.
var LoginStatusFields = []string{
	"name",
	"url",
	"user",
	"valid",
	"auth_method",
	"token_expiry",
	"helper",
	"default",
}

// LoginStatuses prints authentication status for one or more logins.
func LoginStatuses(statuses []LoginStatus, output string) error {
	if output != "" {
		printables := make([]printable, len(statuses))
		for i := range statuses {
			printables[i] = statuses[i]
		}
		t := tableFromItems(LoginStatusFields, printables, isMachineReadable(output))
		return t.print(output)
	}

	if len(statuses) == 0 {
		fmt.Println("No logins configured.")
		return nil
	}

	for i, status := range statuses {
		if i > 0 {
			fmt.Println()
		}
		printLoginStatusReport(status)
	}
	return nil
}

func printLoginStatusReport(status LoginStatus) {
	name := status.Name
	if status.Default {
		name += " (default)"
	}
	fmt.Println(name)

	if status.Valid {
		line := "  ✔ Logged in to " + status.URL
		if status.User != "" {
			line += " as " + status.User
		}
		fmt.Println(line)

		tokenLine := "  ✔ Token is valid"
		if status.AuthMethod != "" {
			tokenLine += " (" + status.AuthMethod
			if !status.TokenExpiry.IsZero() {
				tokenLine += ", " + formatTokenExpiry(status.TokenExpiry)
			}
			tokenLine += ")"
		}
		fmt.Println(tokenLine)
	} else {
		message := status.Error
		if message == "" {
			message = "Login failed"
		}
		fmt.Println("  ✗ " + message)
	}

	if status.Helper {
		fmt.Println("  ✔ Git credential helper configured")
	} else {
		fmt.Println("  ✗ Git credential helper not configured")
	}
}

func formatExpiryDuration(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "expired"
	}
	if d < time.Minute {
		return "in less than a minute"
	}
	return "in " + strings.TrimSuffix(d.Truncate(time.Minute).String(), "0s")
}

func formatTokenExpiry(t time.Time) string {
	if t.Before(time.Now()) {
		return "expired"
	}
	return "expires " + formatExpiryDuration(t)
}

// FormatField implements the printable interface for LoginStatus.
func (s LoginStatus) FormatField(field string, machineReadable bool) string {
	switch field {
	case "name":
		return s.Name
	case "url":
		return s.URL
	case "user":
		return s.User
	case "valid":
		return formatBoolean(s.Valid, !machineReadable)
	case "auth_method":
		return s.AuthMethod
	case "token_expiry":
		if s.TokenExpiry.IsZero() {
			return ""
		}
		if machineReadable {
			return FormatTime(s.TokenExpiry, true)
		}
		return formatExpiryDuration(s.TokenExpiry)
	case "helper":
		return formatBoolean(s.Helper, !machineReadable)
	case "default":
		return formatBoolean(s.Default, !machineReadable)
	}
	return ""
}
