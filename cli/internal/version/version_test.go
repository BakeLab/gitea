// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package version

import (
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatDescribesTheBuild(t *testing.T) {
	stamped := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2026-10-01T09:30:00Z"}}}
	for _, tc := range []struct {
		name, version, tags string
		info                *debug.BuildInfo
		want                string
	}{
		{"integrated release", "trim-abcdef123456-42-1", "", nil, "gt version trim-abcdef123456-42-1\nhttps://gitea.int.bb.pub/BakeLab/gitea/releases/tag/trim-abcdef123456-42-1\nbuilt with go1.27.2"},
		{"release", "1.0.0", "", stamped, "gt version 1.0.0 (2026-10-01)\nhttps://gitea.com/gitea/cli/releases/tag/v1.0.0\nbuilt with go1.27.2"},
		{"release candidate with tags", "1.1.0-rc1", "sqlite netgo", nil, "gt version 1.1.0-rc1\nhttps://gitea.com/gitea/cli/releases/tag/v1.1.0-rc1\nbuilt with go1.27.2 (tags: sqlite, netgo)"},
		{"between releases", "0.16.0+12-gabcdef", "", stamped, "gt version 0.16.0+12-gabcdef (2026-10-01)\nbuilt with go1.27.2"},
		{"go install", "development", "", &debug.BuildInfo{Main: debug.Module{Version: "v1.0.1"}}, "gt version 1.0.1\nhttps://gitea.com/gitea/cli/releases/tag/v1.0.1\nbuilt with go1.27.2"},
		{"local build", "development", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "gt version development\nbuilt with go1.27.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, format(tc.version, tc.tags, "go1.27.2", tc.info))
		})
	}
}

func TestResolveFallsBackWhenUnstamped(t *testing.T) {
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v1.0.1"}}
	require.Equal(t, "1.0.1", resolve("development", installed))
	require.Equal(t, "1.0.1", resolve("", installed))
	require.Equal(t, "development", resolve("", nil))
	require.Equal(t, "development", resolve("", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}))
	require.Equal(t, "1.0.0", resolve("1.0.0", installed))
}

func TestFormatAndCurrentUseTheStamp(t *testing.T) {
	version, tags := Version, Tags
	t.Cleanup(func() { Version, Tags = version, tags })
	Version, Tags = "1.2.3", "netgo"
	require.Equal(t, "1.2.3", Current())
	require.Equal(t, "gt version 1.2.3\nhttps://gitea.com/gitea/cli/releases/tag/v1.2.3\nbuilt with "+runtime.Version()+" (tags: netgo)", Format())
}
