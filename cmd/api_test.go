// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/context"
	tea_git "code.gitea.io/tea/modules/git"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestParseTypedValue(t *testing.T) {
	t.Run("null", func(t *testing.T) {
		v, err := parseTypedValue("null")
		require.NoError(t, err)
		assert.Nil(t, v)
	})

	t.Run("bool true", func(t *testing.T) {
		v, err := parseTypedValue("true")
		require.NoError(t, err)
		assert.Equal(t, true, v)
	})

	t.Run("bool false", func(t *testing.T) {
		v, err := parseTypedValue("false")
		require.NoError(t, err)
		assert.Equal(t, false, v)
	})

	t.Run("integer", func(t *testing.T) {
		v, err := parseTypedValue("42")
		require.NoError(t, err)
		assert.Equal(t, int64(42), v)
	})

	t.Run("float", func(t *testing.T) {
		v, err := parseTypedValue("3.14")
		require.NoError(t, err)
		assert.Equal(t, 3.14, v)
	})

	t.Run("string", func(t *testing.T) {
		v, err := parseTypedValue("hello")
		require.NoError(t, err)
		assert.Equal(t, "hello", v)
	})

	t.Run("JSON array", func(t *testing.T) {
		v, err := parseTypedValue("[1,2,3]")
		require.NoError(t, err)
		assert.Equal(t, []any{float64(1), float64(2), float64(3)}, v)
	})

	t.Run("JSON object", func(t *testing.T) {
		v, err := parseTypedValue(`{"key":"val"}`)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"key": "val"}, v)
	})

	t.Run("invalid JSON array falls back to string", func(t *testing.T) {
		v, err := parseTypedValue("[not json")
		require.NoError(t, err)
		assert.Equal(t, "[not json", v)
	})

	t.Run("invalid JSON object falls back to string", func(t *testing.T) {
		v, err := parseTypedValue("{not json")
		require.NoError(t, err)
		assert.Equal(t, "{not json", v)
	})

	t.Run("file reference", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte("file content\n"), 0o644))
		v, err := parseTypedValue("@" + tmpFile)
		require.NoError(t, err)
		assert.Equal(t, "file content", v)
	})

	t.Run("file reference without trailing newline", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte("no newline"), 0o644))
		v, err := parseTypedValue("@" + tmpFile)
		require.NoError(t, err)
		assert.Equal(t, "no newline", v)
	})

	t.Run("empty file reference", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "empty.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte(""), 0o644))
		v, err := parseTypedValue("@" + tmpFile)
		require.NoError(t, err)
		assert.Equal(t, "", v)
	})

	t.Run("nonexistent file reference", func(t *testing.T) {
		_, err := parseTypedValue("@/nonexistent/file.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read")
	})

	t.Run("negative integer", func(t *testing.T) {
		v, err := parseTypedValue("-42")
		require.NoError(t, err)
		assert.Equal(t, int64(-42), v)
	})

	t.Run("negative float", func(t *testing.T) {
		v, err := parseTypedValue("-3.14")
		require.NoError(t, err)
		assert.Equal(t, -3.14, v)
	})

	t.Run("scientific notation", func(t *testing.T) {
		v, err := parseTypedValue("1.5e10")
		require.NoError(t, err)
		assert.Equal(t, 1.5e10, v)
	})

	t.Run("empty string", func(t *testing.T) {
		v, err := parseTypedValue("")
		require.NoError(t, err)
		assert.Equal(t, "", v)
	})

	t.Run("string starting with number", func(t *testing.T) {
		v, err := parseTypedValue("123abc")
		require.NoError(t, err)
		assert.Equal(t, "123abc", v)
	})

	t.Run("nested JSON object", func(t *testing.T) {
		v, err := parseTypedValue(`{"user":{"name":"alice","id":1}}`)
		require.NoError(t, err)
		expected := map[string]any{
			"user": map[string]any{
				"name": "alice",
				"id":   float64(1),
			},
		}
		assert.Equal(t, expected, v)
	})

	t.Run("complex JSON array", func(t *testing.T) {
		v, err := parseTypedValue(`[{"id":1},{"id":2}]`)
		require.NoError(t, err)
		expected := []any{
			map[string]any{"id": float64(1)},
			map[string]any{"id": float64(2)},
		}
		assert.Equal(t, expected, v)
	})

	t.Run("quoted string prevents type parsing", func(t *testing.T) {
		v, err := parseTypedValue(`"null"`)
		require.NoError(t, err)
		assert.Equal(t, "null", v)
	})

	t.Run("quoted true becomes string", func(t *testing.T) {
		v, err := parseTypedValue(`"true"`)
		require.NoError(t, err)
		assert.Equal(t, "true", v)
	})

	t.Run("quoted false becomes string", func(t *testing.T) {
		v, err := parseTypedValue(`"false"`)
		require.NoError(t, err)
		assert.Equal(t, "false", v)
	})

	t.Run("quoted number becomes string", func(t *testing.T) {
		v, err := parseTypedValue(`"123"`)
		require.NoError(t, err)
		assert.Equal(t, "123", v)
	})

	t.Run("quoted empty string", func(t *testing.T) {
		v, err := parseTypedValue(`""`)
		require.NoError(t, err)
		assert.Equal(t, "", v)
	})

	t.Run("quoted string with spaces", func(t *testing.T) {
		v, err := parseTypedValue(`"hello world"`)
		require.NoError(t, err)
		assert.Equal(t, "hello world", v)
	})

	t.Run("single quote not treated as quote", func(t *testing.T) {
		v, err := parseTypedValue(`'hello'`)
		require.NoError(t, err)
		assert.Equal(t, "'hello'", v)
	})

	t.Run("unmatched quote at start only", func(t *testing.T) {
		v, err := parseTypedValue(`"hello`)
		require.NoError(t, err)
		assert.Equal(t, `"hello`, v)
	})

	t.Run("unmatched quote at end only", func(t *testing.T) {
		v, err := parseTypedValue(`hello"`)
		require.NoError(t, err)
		assert.Equal(t, `hello"`, v)
	})

	t.Run("quoted string with escaped quote", func(t *testing.T) {
		v, err := parseTypedValue(`"hello \"world\""`)
		require.NoError(t, err)
		assert.Equal(t, `hello "world"`, v)
	})

	t.Run("quoted string with backslash-n", func(t *testing.T) {
		v, err := parseTypedValue(`"line1\nline2"`)
		require.NoError(t, err)
		assert.Equal(t, "line1\nline2", v)
	})

	t.Run("quoted string with tab escape", func(t *testing.T) {
		v, err := parseTypedValue(`"col1\tcol2"`)
		require.NoError(t, err)
		assert.Equal(t, "col1\tcol2", v)
	})

	t.Run("quoted string with backslash", func(t *testing.T) {
		v, err := parseTypedValue(`"path\\to\\file"`)
		require.NoError(t, err)
		assert.Equal(t, `path\to\file`, v)
	})

	t.Run("invalid escape sequence in quoted string", func(t *testing.T) {
		_, err := parseTypedValue(`"bad \z escape"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid quoted string")
	})
}

// runApiWithArgs sets up a test server that captures requests, configures the
// login to point at it, and runs the api command with the given CLI args.
// Returns the captured HTTP method, body bytes, and any error from the command.
func runApiWithArgs(t *testing.T, args []string) (method string, body []byte, err error) {
	t.Helper()

	var mu sync.Mutex
	var capturedMethod string
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatalf("failed to read request body: %v", readErr)
		}
		mu.Lock()
		capturedMethod = r.Method
		capturedBody = b
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	config.SetConfigForTesting(config.LocalConfig{
		Logins: []config.Login{{
			Name:    "testLogin",
			URL:     server.URL,
			Token:   "test-token",
			User:    "testUser",
			Default: true,
		}},
	})

	// Use the apiFlags factory to get fresh flag instances, avoiding shared
	// hasBeenSet state between tests. Append minimal login/repo flags needed
	// for the test harness.
	cmd := cli.Command{
		Name:                      "api",
		DisableSliceFlagSeparator: true,
		Action:                    runApi,
		Flags: append(apiFlags(), []cli.Flag{
			&cli.StringFlag{Name: "login", Aliases: []string{"l"}},
			&cli.StringFlag{Name: "repo", Aliases: []string{"r"}},
			&cli.StringFlag{Name: "remote", Aliases: []string{"R"}},
		}...),
		Writer:    io.Discard,
		ErrWriter: io.Discard,
	}

	fullArgs := append([]string{"api", "--login", "testLogin"}, args...)
	runErr := cmd.Run(stdctx.Background(), fullArgs)

	mu.Lock()
	defer mu.Unlock()
	return capturedMethod, capturedBody, runErr
}

func TestApiCommaInFieldValue(t *testing.T) {
	_, body, err := runApiWithArgs(t, []string{"-f", "body=hello, world", "-X", "POST", "/test"})
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
	assert.Equal(t, "hello, world", parsed["body"])
}

func TestApiRawDataFlag(t *testing.T) {
	_, body, err := runApiWithArgs(t, []string{"-d", `{"title":"test","body":"hello"}`, "/test"})
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
	assert.Equal(t, "test", parsed["title"])
	assert.Equal(t, "hello", parsed["body"])
}

func TestApiDataFieldMutualExclusion(t *testing.T) {
	_, _, err := runApiWithArgs(t, []string{"-d", `{"title":"test"}`, "-f", "key=val", "/test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--data/-d cannot be combined with --field/-f or --Field/-F")
}

func TestApiMethodAutoDefault(t *testing.T) {
	t.Run("POST when body provided without explicit method", func(t *testing.T) {
		method, _, err := runApiWithArgs(t, []string{"-d", `{"title":"test"}`, "/test"})
		require.NoError(t, err)
		assert.Equal(t, "POST", method)
	})

	t.Run("explicit method overrides auto-POST", func(t *testing.T) {
		method, _, err := runApiWithArgs(t, []string{"-d", `{"title":"test"}`, "-X", "PATCH", "/test"})
		require.NoError(t, err)
		assert.Equal(t, "PATCH", method)
	})

	t.Run("GET when no body", func(t *testing.T) {
		method, _, err := runApiWithArgs(t, []string{"/test"})
		require.NoError(t, err)
		assert.Equal(t, "GET", method)
	})
}

func TestApiMultipleFields(t *testing.T) {
	t.Run("multiple -f flags", func(t *testing.T) {
		_, body, err := runApiWithArgs(t, []string{
			"-f", "title=Test Issue",
			"-f", "body=Description here",
			"-X", "POST",
			"/test",
		})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Equal(t, "Test Issue", parsed["title"])
		assert.Equal(t, "Description here", parsed["body"])
	})

	t.Run("multiple -F flags with different types", func(t *testing.T) {
		_, body, err := runApiWithArgs(t, []string{
			"-F", "milestone=5",
			"-F", "closed=true",
			"-F", "title=Test",
			"-X", "POST",
			"/test",
		})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Equal(t, float64(5), parsed["milestone"])
		assert.Equal(t, true, parsed["closed"])
		assert.Equal(t, "Test", parsed["title"])
	})

	t.Run("combining -f and -F flags", func(t *testing.T) {
		_, body, err := runApiWithArgs(t, []string{
			"-f", "title=Test",
			"-F", "milestone=3",
			"-F", "closed=false",
			"-X", "POST",
			"/test",
		})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Equal(t, "Test", parsed["title"])
		assert.Equal(t, float64(3), parsed["milestone"])
		assert.Equal(t, false, parsed["closed"])
	})

	t.Run("-F with JSON array", func(t *testing.T) {
		_, body, err := runApiWithArgs(t, []string{
			"-F", `labels=["bug","enhancement"]`,
			"-X", "POST",
			"/test",
		})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Equal(t, []any{"bug", "enhancement"}, parsed["labels"])
	})

	t.Run("-F with JSON object", func(t *testing.T) {
		_, body, err := runApiWithArgs(t, []string{
			"-F", `assignee={"login":"alice","id":123}`,
			"-X", "POST",
			"/test",
		})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		assignee, ok := parsed["assignee"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "alice", assignee["login"])
		assert.Equal(t, float64(123), assignee["id"])
	})

	t.Run("-F with quoted string to prevent type parsing", func(t *testing.T) {
		_, body, err := runApiWithArgs(t, []string{
			"-F", `status="null"`,
			"-F", `enabled="true"`,
			"-F", `count="42"`,
			"-X", "POST",
			"/test",
		})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Equal(t, "null", parsed["status"])
		assert.Equal(t, "true", parsed["enabled"])
		assert.Equal(t, "42", parsed["count"])
	})
}

func TestApiDataFromFile(t *testing.T) {
	t.Run("read JSON from file", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "data.json")
		jsonData := `{"title":"From File","body":"File content"}`
		require.NoError(t, os.WriteFile(tmpFile, []byte(jsonData), 0o644))

		_, body, err := runApiWithArgs(t, []string{"-d", "@" + tmpFile, "/test"})
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Equal(t, "From File", parsed["title"])
		assert.Equal(t, "File content", parsed["body"])
	})

	t.Run("invalid JSON in --data flag", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{"-d", `{invalid json}`, "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not valid JSON")
	})

	t.Run("invalid JSON from file includes filename", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "bad.json")
		require.NoError(t, os.WriteFile(tmpFile, []byte("not json"), 0o644))

		_, _, err := runApiWithArgs(t, []string{"-d", "@" + tmpFile, "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not valid JSON")
		assert.Contains(t, err.Error(), "bad.json")
	})
}

func TestApiErrorHandling(t *testing.T) {
	t.Run("missing endpoint argument", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "endpoint argument required")
	})

	t.Run("invalid field format", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{"-f", "invalidformat", "-X", "POST", "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid field format")
	})

	t.Run("invalid Field format", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{"-F", "noequalsign", "-X", "POST", "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid field format")
	})

	t.Run("empty field key with -f", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{"-f", "=value", "-X", "POST", "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "field key cannot be empty")
	})

	t.Run("empty field key with -F", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{"-F", "=123", "-X", "POST", "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "field key cannot be empty")
	})

	t.Run("duplicate field key in -f flags", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{"-f", "key=first", "-f", "key=second", "-X", "POST", "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate field key")
	})

	t.Run("duplicate field key in -F flags", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{"-F", "key=1", "-F", "key=2", "-X", "POST", "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate field key")
	})

	t.Run("duplicate field key across -f and -F flags", func(t *testing.T) {
		_, _, err := runApiWithArgs(t, []string{"-f", "key=string", "-F", "key=123", "-X", "POST", "/test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate field key")
	})
}

func TestExpandPlaceholders(t *testing.T) {
	t.Run("replaces owner and repo", func(t *testing.T) {
		ctx := &context.TeaContext{
			Owner: "myorg",
			Repo:  "myrepo",
		}
		result := expandPlaceholders("/repos/{owner}/{repo}/issues", ctx)
		assert.Equal(t, "/repos/myorg/myrepo/issues", result)
	})

	t.Run("replaces multiple occurrences", func(t *testing.T) {
		ctx := &context.TeaContext{
			Owner: "alice",
			Repo:  "proj",
		}
		result := expandPlaceholders("/repos/{owner}/{repo}/branches?owner={owner}", ctx)
		assert.Equal(t, "/repos/alice/proj/branches?owner=alice", result)
	})

	t.Run("no placeholders returns unchanged", func(t *testing.T) {
		ctx := &context.TeaContext{
			Owner: "alice",
			Repo:  "proj",
		}
		result := expandPlaceholders("/api/v1/version", ctx)
		assert.Equal(t, "/api/v1/version", result)
	})

	t.Run("empty owner and repo produce empty replacements", func(t *testing.T) {
		ctx := &context.TeaContext{}
		result := expandPlaceholders("/repos/{owner}/{repo}", ctx)
		assert.Equal(t, "/repos//", result)
	})

	t.Run("branch left unreplaced when no local repo", func(t *testing.T) {
		ctx := &context.TeaContext{
			Owner: "alice",
			Repo:  "proj",
		}
		result := expandPlaceholders("/repos/{owner}/{repo}/branches/{branch}", ctx)
		assert.Equal(t, "/repos/alice/proj/branches/{branch}", result)
	})

	t.Run("replaces branch from local repo HEAD", func(t *testing.T) {
		tmpDir := t.TempDir()
		repo, err := gogit.PlainInit(tmpDir, false)
		require.NoError(t, err)

		// Create an initial commit so HEAD points to a branch.
		wt, err := repo.Worktree()
		require.NoError(t, err)
		tmpFile := filepath.Join(tmpDir, "init.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte("init"), 0o644))
		_, err = wt.Add("init.txt")
		require.NoError(t, err)
		_, err = wt.Commit("initial commit", &gogit.CommitOptions{
			Author: &object.Signature{Name: "test", Email: "test@test.com"},
		})
		require.NoError(t, err)

		// Create and checkout a feature branch.
		headRef, err := repo.Head()
		require.NoError(t, err)
		branchRef := plumbing.NewBranchReferenceName("feature/my-branch")
		ref := plumbing.NewHashReference(branchRef, headRef.Hash())
		require.NoError(t, repo.Storer.SetReference(ref))
		require.NoError(t, wt.Checkout(&gogit.CheckoutOptions{Branch: branchRef}))

		ctx := &context.TeaContext{
			Owner:     "alice",
			Repo:      "proj",
			LocalRepo: &tea_git.TeaRepo{Repository: repo},
		}
		result := expandPlaceholders("/repos/{owner}/{repo}/branches/{branch}", ctx)
		assert.Equal(t, "/repos/alice/proj/branches/feature/my-branch", result)
	})
}

func TestIsTextContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"empty string defaults to text", "", true},
		{"plain text", "text/plain", true},
		{"html", "text/html", true},
		{"json", "application/json", true},
		{"json with charset", "application/json; charset=utf-8", true},
		{"xml", "application/xml", true},
		{"javascript", "application/javascript", true},
		{"yaml", "application/yaml", true},
		{"toml", "application/toml", true},
		{"binary", "application/octet-stream", false},
		{"image", "image/png", false},
		{"pdf", "application/pdf", false},
		{"zip", "application/zip", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTextContentType(tt.contentType)
			assert.Equal(t, tt.want, got)
		})
	}
}
