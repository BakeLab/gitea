// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/require"
)

func TestSDKCallsAuthenticateWithoutVersionProbe(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/branches/main", r.URL.Path)
		require.Equal(t, "token secret", r.Header.Get("Authorization"))
		require.Equal(t, "bob", r.Header.Get("Sudo"))
		require.Equal(t, transport.UserAgent, r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusNoContent)
	}))
	client.Token, client.Sudo = "secret", "bob"
	renamed, _, err := call(context.Background(), client, func(ctx context.Context, c *gitea.Client) (bool, *gitea.Response, error) {
		return c.Repositories.RenameRepoBranch(ctx, "alice", "project", "main", gitea.RenameRepoBranchOption{Name: "trunk"})
	})
	require.NoError(t, err)
	require.True(t, renamed)
}

// requireAPIError checks the request and server message an *transport.APIError reports.
func requireAPIError(t *testing.T, want transport.APIError, err error) {
	t.Helper()
	got, ok := errors.AsType[*transport.APIError](err)
	require.True(t, ok, "%v is not an API error", err)
	require.Equal(t, want, transport.APIError{Status: got.Status, Method: got.Method, Endpoint: got.Endpoint, Message: got.Message})
}

func TestSDKErrorsMapToAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, message string
		status              int
	}{
		{"not found", `{"message":"repository does not exist"}`, "repository does not exist", http.StatusNotFound},
		{"method refused", `{"message":"Please try again later"}`, "Please try again later", http.StatusMethodNotAllowed},
		{"no message", `<html>oops</html>`, "", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			client.Token = "secret"
			err := do(context.Background(), client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
				return c.Repositories.DeleteRepo(ctx, "alice", "project")
			})
			requireAPIError(t, transport.APIError{Status: tc.status, Method: http.MethodDelete, Endpoint: "/api/v1/repos/alice/project", Message: tc.message}, err)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestSDKStatusOnlyFailureKeepsServerMessage(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"branch trunk already exists"}`))
	}))
	renamed, _, err := call(context.Background(), client, func(ctx context.Context, c *gitea.Client) (bool, *gitea.Response, error) {
		return c.Repositories.RenameRepoBranch(ctx, "alice", "project", "main", gitea.RenameRepoBranchOption{Name: "trunk"})
	})
	require.False(t, renamed)
	requireAPIError(t, transport.APIError{Status: http.StatusUnprocessableEntity, Method: http.MethodPatch, Endpoint: "/api/v1/repos/alice/project/branches/main", Message: "branch trunk already exists"}, err)
	require.EqualError(t, err, "Gitea API returned HTTP 422 for PATCH /api/v1/repos/alice/project/branches/main: branch trunk already exists")
}

func TestSDKFailureWithoutResponseKeepsCapturedError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"repository does not exist"}`))
	}))
	_, _, err := call(context.Background(), client, func(ctx context.Context, c *gitea.Client) (*gitea.Repository, *gitea.Response, error) {
		_, _, err := c.Repositories.GetRepo(ctx, "alice", "project")
		return nil, nil, err
	})
	requireAPIError(t, transport.APIError{Status: http.StatusNotFound, Method: http.MethodGet, Endpoint: "/api/v1/repos/alice/project", Message: "repository does not exist"}, err)
}

func TestSDKInvalidJSONIsReportedWithoutBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"login":`))
	}))
	_, _, err := call(context.Background(), client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetMyUserInfo(ctx)
	})
	require.ErrorIs(t, err, errInvalidJSON)
	client = newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"login":42}`))
	}))
	_, _, err = call(context.Background(), client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetMyUserInfo(ctx)
	})
	require.ErrorIs(t, err, errInvalidJSON)
}

func TestSDKCancellationReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	_, _, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetMyUserInfo(ctx)
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestSDKNetworkErrorsOmitRequestURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	_, _, err := call(context.Background(), transport.New(server.URL, "", false), func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetUserInfo(ctx, "query-secret")
	})
	var opErr *net.OpError
	require.ErrorAs(t, err, &opErr)
	require.ErrorContains(t, err, "API request failed: ")
	require.NotContains(t, err.Error(), "query-secret")
}

func TestSDKValidationErrorsPassThrough(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("invalid input must not reach the server")
	}))
	err := do(context.Background(), client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Repositories.DeleteRepo(ctx, "", "project")
	})
	require.Error(t, err)
	var apiErr *transport.APIError
	require.NotErrorAs(t, err, &apiErr)
}

func TestSplitRepository(t *testing.T) {
	owner, name, err := splitRepository("alice/project")
	require.NoError(t, err)
	require.Equal(t, []string{"alice", "project"}, []string{owner, name})
	for _, invalid := range []string{"", "alice", "/project", "alice/", "alice/project/extra"} {
		_, _, err := splitRepository(invalid)
		require.Error(t, err, invalid)
	}
}

func TestServiceFailuresReachTheCaller(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	ctx := context.Background()
	for name, run := range map[string]func() error{
		"branch delete":   func() error { return (Branches{Client: client}).Delete(ctx, "alice/project", "main") },
		"repository view": func() error { _, err := (Repositories{Client: client}).View(ctx, "alice/project"); return err },
		"ssh key view":    func() error { _, err := (SSHKeys{Client: client}).View(ctx, 9); return err },
	} {
		var apiErr *transport.APIError
		require.ErrorAs(t, run(), &apiErr, name)
		require.Equal(t, "not found", apiErr.Message, name)
	}
	for name, run := range map[string]func() error{
		"repository delete": func() error { return (Repositories{Client: client}).Delete(ctx, "alice") },
		"workflows":         func() error { _, err := (Actions{Client: client, Repository: "alice"}).Workflows(ctx); return err },
	} {
		require.ErrorContains(t, run(), "use owner/name", name)
	}
}

func TestSDKResponseHelpers(t *testing.T) {
	require.Equal(t, gitea.ListOptions{Page: 3, PageSize: 20}, listOptions(20, 3))
	require.Empty(t, headerOf(nil))
	require.Empty(t, headerOf(&gitea.Response{}))
	require.Equal(t, "2", headerOf(&gitea.Response{Response: &http.Response{Header: http.Header{"X-Total-Count": {"2"}}}}).Get("X-Total-Count"))
	require.Empty(t, timestamp(time.Time{}))
	require.Nil(t, optionalTimestamp(nil))
	require.Nil(t, optionalTimestamp(&time.Time{}))
	moment := time.Date(2026, 10, 1, 12, 0, 0, 0, time.FixedZone("", 2*60*60))
	require.Equal(t, "2026-10-01T12:00:00+02:00", timestamp(moment))
	require.Equal(t, "2026-10-01T12:00:00+02:00", *optionalTimestamp(&moment))
	require.Empty(t, login(nil))
	require.Empty(t, deref[string](nil))
	name := "alice"
	require.Equal(t, "alice", deref(&name))
}
