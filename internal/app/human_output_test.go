// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/terminal"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type recordingPager struct {
	command string
	input   string
	calls   int
}

func (p *recordingPager) Page(_ context.Context, command string, input io.Reader, streams terminal.Streams) error {
	p.calls++
	p.command = command
	data, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	p.input = string(data)
	_, err = io.WriteString(streams.Out, "PAGED:"+string(data))
	return err
}

// pagedDeps returns serverDeps on a terminal stdout with a recording pager,
// extra environment variables and the given config settings.
func pagedDeps(t *testing.T, serverURL string, env map[string]string, config ...string) (Dependencies, *bytes.Buffer, *bytes.Buffer, *recordingPager) {
	t.Helper()
	deps, out, diagnostics := testDeps(t)
	pager := &recordingPager{}
	deps.Pager = pager
	deps.Streams.OutTTY = true
	deps.Getenv = serverEnv(serverURL, env)
	for i := 0; i < len(config); i += 2 {
		require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", config[i], config[i+1]}))
	}
	return deps, out, diagnostics, pager
}

func TestLabelListAppliesColorAndPagesWithNoInput(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/labels", r.URL.Path)
		_, _ = w.Write([]byte(`[{"id":1,"name":"bug","color":"ff0000","description":"Defect"},{"id":2,"name":"docs","color":"00ff00","description":"Docs"}]`))
	}))
	defer server.Close()
	deps, out, diagnostics, pager := pagedDeps(t, server.URL, map[string]string{"PAGER": "fake-pager", "COLORTERM": "truecolor"}, "color", "always")
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, 1, pager.calls)
	require.Equal(t, "fake-pager", pager.command)
	require.Contains(t, pager.input, "\x1b[38;2;255;0;0mbug\x1b[0m")
	require.Contains(t, pager.input, "\x1b[38;2;0;255;0mdocs\x1b[0m")
	require.Equal(t, "PAGED:"+pager.input, out.String())
	require.Empty(t, diagnostics.String())
}

func TestIssueListRespectsColorNeverAndStillPages(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"number":1,"title":"One","state":"open"},{"number":2,"title":"Two","state":"closed"}]`))
	}))
	defer server.Close()
	deps, out, _, pager := pagedDeps(t, server.URL, nil, "color", "never", "pager", "configured-pager")
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, 1, pager.calls)
	require.Equal(t, "configured-pager", pager.command)
	require.NotContains(t, pager.input, "\x1b[")
	require.Contains(t, pager.input, "open")
	require.Contains(t, pager.input, "closed")
	require.Equal(t, "PAGED:"+pager.input, out.String())
}

func TestIssueViewJSONDoesNotPage(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"number":7,"title":"Title","html_url":"https://example.org/alice/project/issues/7","body":"line1\nline2\nline3"}`)
	}))
	defer server.Close()
	deps, out, _, pager := pagedDeps(t, server.URL, map[string]string{"PAGER": "fake-pager"})
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "view", "7", "-R", "alice/project", "--json=number,title", "--no-input"}))
	require.Zero(t, pager.calls)
	require.JSONEq(t, `{"number":7,"title":"Title"}`, withoutANSI(out.String()))
}

func TestConfiguredPagerOverridesPagerEnvironment(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"number":1,"title":"One"},{"number":2,"title":"Two"}]`))
	}))
	defer server.Close()
	deps, _, _, pager := pagedDeps(t, server.URL, map[string]string{"PAGER": "env-pager"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "env-pager", pager.command)
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "pager", "configured-pager"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "configured-pager", pager.command)
}

func TestTeaPagerAndColorEnvironmentOverrides(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"name":"bug","color":"ff0000","description":""},{"id":2,"name":"docs","color":"00ff00","description":""}]`))
	}))
	defer server.Close()
	env := map[string]string{"GITEA_CLI_PAGER": "gt-pager", "PAGER": "generic-pager", "GITEA_CLI_COLOR": "always"}
	deps, out, _, pager := pagedDeps(t, server.URL, env, "pager", "configured-pager", "color", "never")
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "gt-pager", pager.command)
	require.Contains(t, pager.input, "\x1b[38;5;196mbug\x1b[0m")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "pager", "--show-source", "--json"}))
	require.JSONEq(t, `{"pager":{"value":"gt-pager","source":"GITEA_CLI_PAGER"}}`, withoutANSI(out.String()))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "color", "--show-source", "--json"}))
	require.JSONEq(t, `{"color":{"value":"always","source":"GITEA_CLI_COLOR"}}`, withoutANSI(out.String()))
}

func TestColorAutoUsesStdoutTTY(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"name":"bug","color":"ff0000","description":""}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Streams.OutTTY = false
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "1\tbug\tff0000\t\n", out.String())

	out.Reset()
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "-R", "alice/project", "--no-input"}))
	require.Contains(t, out.String(), "\x1b[38;5;196mbug\x1b[0m")
}

func TestMultiItemHumanOutputPagesOnce(t *testing.T) {
	issue := func(n int) string {
		return fmt.Sprintf(`{"number":%d,"title":"Issue %d","state":"closed","html_url":"https://example.org/i/%d"}`, n, n, n)
	}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/milestones/3":
			_, _ = w.Write([]byte(`{"id":3,"title":"v1"}`))
		case "/api/v1/repos/alice/project/issues":
			_, _ = w.Write([]byte("[" + issue(1) + "," + issue(2) + "]"))
		case "/api/v1/repos/alice/project/issues/1", "/api/v1/repos/alice/project/issues/2":
			_, _ = w.Write([]byte(issue(int(r.URL.Path[len(r.URL.Path)-1] - '0'))))
		case "/api/v1/repos/alice/project/branches":
			_, _ = w.Write([]byte(`[{"name":"main","commit":{"id":"abc"}},{"name":"dev","commit":{"id":"def"}}]`))
		case "/api/v1/repos/alice/project/issues/5/comments":
			_, _ = w.Write([]byte(`[{"id":1,"body":"one","user":{"login":"alice"}},{"id":2,"body":"two","user":{"login":"bob"}}]`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"milestone", "issue", "list", "3"}, "1  Issue 1  closed\n2  Issue 2  closed\n"},
		{[]string{"issue", "close", "1", "2"}, "1  Issue 1  closed\n2  Issue 2  closed\n"},
		{[]string{"branch", "list"}, "main  abc  false\ndev   def  false\n"},
		{[]string{"issue", "comment", "list", "5"}, "1 alice\none\n\n2 bob\ntwo\n\n"},
	} {
		t.Run(tc.args[0]+" "+tc.args[1], func(t *testing.T) {
			deps, out, _, pager := pagedDeps(t, server.URL, map[string]string{"GITEA_CLI_HYPERLINKS": "never"}, "pager", "fake-pager", "color", "never")
			require.NoError(t, Execute(context.Background(), deps, append(tc.args, "-R", "alice/project", "--yes")))
			require.Equal(t, 1, pager.calls)
			require.Equal(t, tc.want, pager.input)
			require.Equal(t, "PAGED:"+tc.want, out.String())
		})
	}
}

func TestListsHintWhenMorePagesExist(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, total := min(limit, 50), 120
		items := []string{}
		for id := (page-1)*size + 1; id <= min(page*size, total); id++ {
			items = append(items, fmt.Sprintf(`{"id":%d,"username":"org%d","full_name":"owner/repo%d"}`, id, id, id))
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	for _, args := range [][]string{{"org", "list"}, {"repo", "list"}, {"issue", "list", "-R", "alice/project"}, {"pr", "list", "-R", "alice/project"}} {
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "--limit", "100")))
		require.Len(t, strings.Split(strings.TrimSpace(out.String()), "\n"), 100, args)
		require.Equal(t, "more results available; use --page 2\n", diagnostics.String(), args)
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "--limit", "100", "--page", "2")))
		require.Len(t, strings.Split(strings.TrimSpace(out.String()), "\n"), 20, args)
		require.Empty(t, diagnostics.String(), args)
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "--json=id")))
		require.Empty(t, diagnostics.String(), args)
	}
}

func TestListsFitTheTerminalAndSplitWhenPiped(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"number":6,"title":"A very long issue title that keeps going and going to see how list output handles wrapping","state":"open","user":{"login":"alice"}},` +
			`{"number":5,"title":"支持中文路径 (support CJK paths)","state":"open","user":{"login":""}}]`))
	}))
	defer server.Close()
	for _, group := range []string{"issue", "pr"} {
		t.Run(group, func(t *testing.T) {
			deps, out, _ := serverDeps(t, server.URL)
			args := []string{group, "list", "-R", "alice/project"}
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, "6\tA very long issue title that keeps going and going to see how list output handles wrapping\topen\talice\n"+
				"5\t支持中文路径 (support CJK paths)\topen\t\n", out.String())

			out.Reset()
			deps.Streams.OutTTY, deps.Streams.Width = true, 50
			deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, "ID  TITLE                            STATE  AUTHOR\n"+
				"#6  A very long issue title that k…  open   alice\n"+
				"#5  支持中文路径 (support CJK path…  open\n", stripLinks(out.String()))
			for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
				require.LessOrEqual(t, runewidth.StringWidth(stripLinks(line)), 50, line)
			}
		})
	}
}

// stripLinks removes OSC 8 hyperlinks from rendered output.
func stripLinks(s string) string {
	for {
		start := strings.Index(s, "\x1b]8;")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "\x1b\\")
		s = s[:start] + s[start+end+2:]
	}
}

// outputApplication returns an application over deps and a command writing to its streams.
func outputApplication(deps Dependencies, json string) (*application, *cobra.Command) {
	deps.Now = func() time.Time { return time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC) }
	a := &application{deps: deps, options: options{json: json}}
	cmd := &cobra.Command{Use: "list"}
	cmd.SetOut(deps.Streams.Out)
	cmd.SetErr(deps.Streams.ErrOut)
	cmd.SetContext(context.Background())
	return a, cmd
}

type listed struct {
	Number  int64     `json:"number"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
}

var listedSpec = listSpec[listed]{
	Columns: []terminal.Column{{Header: "ID", Prefix: "#"}, {Header: "Title", Flex: true}, {Header: "Updated"}},
	Row: func(item listed) []terminal.Cell {
		return []terminal.Cell{{Text: strconv.FormatInt(item.Number, 10)}, {Text: item.Title}, {Time: item.Updated}}
	},
	Empty:   "no open issues in alice/x",
	Context: "open issues in alice/x",
}

func TestWriteTable(t *testing.T) {
	updated := time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC)
	items := []listed{{12, "Emoji in title 🚀", updated}, {1, "Login fails", updated}}
	for _, tc := range []struct {
		name          string
		tty           bool
		json          string
		items         []listed
		page          listPage
		out, stderr   string
		pagedHeadings bool
	}{
		{
			name: "context line and header on a terminal", tty: true, items: items, page: listPage{Number: 1, More: true, Total: 112, Counted: true},
			out: "Showing 2 of 112 open issues in alice/x · next: --page 2\n\n" +
				"ID   TITLE              UPDATED\n#12  Emoji in title 🚀  about 2 hours ago\n#1   Login fails        about 2 hours ago\n",
		},
		{
			name: "counts a single page without a total", tty: true, items: items[:1], page: listPage{Number: 1},
			out: "Showing 1 of 1 open issues in alice/x\n\nID   TITLE              UPDATED\n#12  Emoji in title 🚀  about 2 hours ago\n",
		},
		{
			name: "tab-separated with the page hint on stderr when piped", items: items, page: listPage{Number: 2, More: true},
			out:    "12\tEmoji in title 🚀\t2026-10-11T18:00:00Z\n1\tLogin fails\t2026-10-11T18:00:00Z\n",
			stderr: "more results available; use --page 3\n",
		},
		{name: "empty message on a terminal", tty: true, page: listPage{Number: 1}, stderr: "no open issues in alice/x\n"},
		{name: "nothing when empty and piped", page: listPage{Number: 1}},
		{name: "JSON", tty: true, json: "number", items: items[:1], page: listPage{Number: 1, More: true}, out: "[\n  {\n    \"number\": 12\n  }\n]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, diagnostics := testDeps(t)
			deps.Streams.OutTTY = tc.tty
			deps.Getenv = envFunc(map[string]string{"NO_COLOR": "1"})
			a, cmd := outputApplication(deps, tc.json)
			require.NoError(t, writeTable(a, cmd, tc.items, listedSpec, tc.page))
			require.Equal(t, tc.out, out.String())
			require.Equal(t, tc.stderr, diagnostics.String())
		})
	}
}

func TestWriteView(t *testing.T) {
	view := func(item listed) terminal.View {
		return terminal.View{
			Title: item.Title, ID: fmt.Sprintf("#%d", item.Number), Badges: []terminal.Cell{{Text: "Open", Color: "green"}},
			Meta: "alice opened " + terminal.RelativeTime(time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC), item.Updated), Body: "Steps", Kind: "issue", URL: "https://example.org/1",
		}
	}
	item := listed{1, "Login fails", time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		name, json, want string
		tty              bool
	}{
		{name: "terminal", tty: true, want: "Login fails #1\nOpen • alice opened about 2 hours ago\n\nSteps\n\nView this issue on Gitea: https://example.org/1\n"},
		{name: "piped", want: "title:\tLogin fails\nurl:\thttps://example.org/1\n--\nSteps\n"},
		{name: "JSON", tty: true, json: "title", want: "{\n  \"title\": \"Login fails\"\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := testDeps(t)
			deps.Streams.OutTTY = tc.tty
			deps.Getenv = envFunc(map[string]string{"NO_COLOR": "1", "GITEA_CLI_MARKDOWN": "raw", "GITEA_CLI_HYPERLINKS": "never"})
			a, cmd := outputApplication(deps, tc.json)
			require.NoError(t, writeView(a, cmd, item, view))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestMutationFeedback(t *testing.T) {
	for _, tc := range []struct {
		name, json, out, stderr string
		tty                     bool
	}{
		{name: "terminal", tty: true, out: "https://example.org/12\n", stderr: "✓ Created issue alice/x#12 (Fix[2J login)\n! Issue alice/x#12 (Fix[2J login) is already closed\n"},
		{name: "piped", out: "https://example.org/12\n"},
		{name: "JSON", tty: true, json: "number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, diagnostics := testDeps(t)
			deps.Streams.OutTTY = tc.tty
			deps.Getenv = envFunc(map[string]string{"NO_COLOR": "1"})
			a, cmd := outputApplication(deps, tc.json)
			ref := refLabel("alice/x", 12, "Fix\x1b[2J login")
			a.done(cmd, "Created issue %s", ref)
			a.warn(cmd, "Issue %s is already closed", ref)
			require.NoError(t, a.printURL(cmd, "https://example.org/12"))
			require.Equal(t, tc.out, out.String())
			require.Equal(t, tc.stderr, diagnostics.String())
		})
	}
	deps, _, diagnostics := testDeps(t)
	deps.Streams.OutTTY = true
	a, cmd := outputApplication(deps, "")
	a.done(cmd, "Closed issue %s", "alice/x#5 (Bug)")
	a.warn(cmd, "nothing to do")
	require.Equal(t, "\x1b[32m✓\x1b[0m Closed issue alice/x#5 (Bug)\n\x1b[33m!\x1b[0m nothing to do\n", diagnostics.String())
}

func TestBatchDoneReportsEachSuccess(t *testing.T) {
	run := func(n int64) (listed, error) {
		if n == 2 {
			return listed{}, errors.New("not found")
		}
		return listed{Number: n, Title: fmt.Sprintf("Issue %d", n)}, nil
	}
	label := func(n int64) string { return fmt.Sprintf("#%d", n) }
	report := func(item listed) string { return "Closed issue " + refLabel("alice/x", item.Number, item.Title) }
	for _, tc := range []struct {
		name, json, out, stderr string
		tty                     bool
	}{
		{name: "terminal", tty: true, stderr: "✓ Closed issue alice/x#1 (Issue 1)\n✓ Closed issue alice/x#3 (Issue 3)\n"},
		{name: "piped"},
		{name: "JSON", json: "number", out: `[{"number":1},{"number":3}]` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, diagnostics := testDeps(t)
			deps.Streams.OutTTY = tc.tty
			deps.Getenv = envFunc(map[string]string{"NO_COLOR": "1"})
			a, cmd := outputApplication(deps, tc.json)
			err := batchDone(a, cmd, []int64{1, 2, 3}, label, run, report)
			require.EqualError(t, err, "#2: not found")
			require.Equal(t, tc.out, out.String())
			require.Equal(t, tc.stderr, diagnostics.String())
		})
	}
}

func TestShowing(t *testing.T) {
	for _, tc := range []struct {
		shown int
		page  listPage
		want  string
	}{
		{30, listPage{Number: 1, More: true, Total: 112, Counted: true}, "Showing 30 of 112 open issues · next: --page 2"},
		{11, listPage{Number: 1, Total: 11, Counted: true}, "Showing 11 of 11 open issues"},
		{11, listPage{Number: 1}, "Showing 11 of 11 open issues"},
		{30, listPage{Number: 1, More: true}, "Showing 30 open issues · next: --page 2"},
		{5, listPage{Number: 3}, "Showing 5 open issues"},
	} {
		require.Equal(t, tc.want, showing(tc.shown, tc.page, "open issues"), "%+v", tc.page)
	}
}
