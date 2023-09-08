// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// IsInteractive checks if the output is piped, but NOT if the session is run interactively..
func IsInteractive() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// captures the repo URL part <host>/<owner>/<repo> of an url
var repoURLRegex = regexp.MustCompile("^([[:alnum:]]+://[^/]+(?:/[[:alnum:]]+){2})/.*")

func getRepoURL(resourceURL string) string {
	return repoURLRegex.ReplaceAllString(resourceURL, "$1/")
}

// formatSize get kb in int and return string
func formatSize(kb int64) string {
	if kb < 1024 {
		return fmt.Sprintf("%d Kb", kb)
	}
	mb := kb / 1024
	if mb < 1024 {
		return fmt.Sprintf("%d Mb", mb)
	}
	gb := mb / 1024
	if gb < 1024 {
		return fmt.Sprintf("%d Gb", gb)
	}
	return fmt.Sprintf("%d Tb", gb/1024)
}

// FormatTime provides a string for the given time value.
// If machineReadable is set, a UTC RFC3339 string is returned,
// otherwise a simplified string in local time is used.
func FormatTime(t time.Time, machineReadable bool) string {
	if t.IsZero() {
		return ""
	}

	if machineReadable {
		return t.UTC().Format(time.RFC3339)
	}

	location, err := time.LoadLocation("Local")
	if err != nil {
		return t.Format("2006-01-02 15:04 UTC")
	}
	return t.In(location).Format("2006-01-02 15:04")
}

func formatDuration(seconds int64, outputType string) string {
	if isMachineReadable(outputType) {
		return fmt.Sprint(seconds)
	}
	return time.Duration(1e9 * seconds).String()
}

func formatLabel(label *gitea.Label, allowColor bool, text string) string {
	colorProfile := termenv.Ascii
	if allowColor {
		colorProfile = termenv.EnvColorProfile()
	}
	if len(text) == 0 {
		text = label.Name
	}
	styled := termenv.String(text)
	styled = styled.Foreground(colorProfile.Color("#" + label.Color))
	return fmt.Sprint(styled)
}

func formatPermission(p *gitea.Permission) string {
	if p.Admin {
		return "admin"
	} else if p.Push {
		return "write"
	}
	return "read"
}

func formatUserName(u *gitea.User) string {
	if len(u.FullName) == 0 {
		return u.UserName
	}
	return u.FullName
}

func formatBoolean(b bool, allowIcons bool) string {
	if !allowIcons {
		return fmt.Sprintf("%v", b)
	}

	styled := "✔"
	if !b {
		styled = "✖"
	}

	return styled
}
