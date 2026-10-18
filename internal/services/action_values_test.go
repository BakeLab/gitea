// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var actionValueScopes = []struct {
	name  string
	scope ActionScope
	path  string
}{
	{"repository", ActionScope{Repository: "alice/project"}, "/api/v1/repos/alice/project/actions"},
	{"organization", ActionScope{Organization: "team"}, "/api/v1/orgs/team/actions"},
	{"account", ActionScope{Account: true}, "/api/v1/user/actions"},
}

func TestVariableSetSelectsCreateOrUpdate(t *testing.T) {
	for _, scope := range actionValueScopes {
		for _, test := range []struct {
			read       int
			wantMethod string
		}{{http.StatusOK, http.MethodPut}, {http.StatusNotFound, http.MethodPost}, {http.StatusForbidden, ""}} {
			t.Run(fmt.Sprintf("%s/%d", scope.name, test.read), func(t *testing.T) {
				var mutations []string
				client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, scope.path+"/variables/BUILD_MODE", r.URL.Path)
					if r.Method == http.MethodGet {
						w.WriteHeader(test.read)
						_, _ = w.Write([]byte(`{"name":"BUILD_MODE","data":"old","message":"variable not found"}`))
						return
					}
					mutations = append(mutations, r.Method)
					var payload map[string]string
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					assert.Equal(t, "new\nvalue", payload["value"])
					assert.Equal(t, "build flavor", payload["description"])
					w.WriteHeader(http.StatusNoContent)
				}))
				item, err := (ActionVariables{Client: client, Scope: scope.scope}).Set(context.Background(), "BUILD_MODE", "new\nvalue", new("build flavor"))
				if test.wantMethod == "" {
					var apiErr *transport.APIError
					require.ErrorAs(t, err, &apiErr)
					require.Equal(t, http.StatusForbidden, apiErr.Status)
					require.Empty(t, mutations, "a failed read must not mutate")
					return
				}
				require.NoError(t, err)
				require.Equal(t, []string{test.wantMethod}, mutations)
				require.Equal(t, ActionVariable{Name: "BUILD_MODE", Value: "new\nvalue", Description: "build flavor"}, item)
			})
		}
	}
}

func TestVariableReadsAndDeletesUseTheirScope(t *testing.T) {
	for _, scope := range actionValueScopes {
		t.Run(scope.name, func(t *testing.T) {
			var requests []string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch {
				case r.Method == http.MethodDelete:
					w.WriteHeader(http.StatusNoContent)
				case r.URL.Path == scope.path+"/variables":
					assert.Equal(t, "20", r.URL.Query().Get("limit"))
					assert.Equal(t, "2", r.URL.Query().Get("page"))
					_, _ = w.Write([]byte(`[{"name":"MODE","data":"test","description":"build mode","owner_id":1}]`))
				default:
					_, _ = w.Write([]byte(`{"name":"MODE","data":"test","description":"build mode"}`))
				}
			}))
			service := ActionVariables{Client: client, Scope: scope.scope}
			want := ActionVariable{Name: "MODE", Value: "test", Description: "build mode"}
			items, err := service.ListPage(context.Background(), 20, 2)
			require.NoError(t, err)
			require.Equal(t, []ActionVariable{want}, items.Items)
			item, err := service.View(context.Background(), "MODE")
			require.NoError(t, err)
			require.Equal(t, want, item)
			require.NoError(t, service.Delete(context.Background(), "MODE"))
			require.Equal(t, []string{
				"GET " + scope.path + "/variables", "GET " + scope.path + "/variables/MODE", "DELETE " + scope.path + "/variables/MODE",
			}, requests)
		})
	}
}

func TestSecretMutationsUseTheirScope(t *testing.T) {
	for _, scope := range actionValueScopes {
		t.Run(scope.name, func(t *testing.T) {
			var requests []string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.EscapedPath())
				if r.Method == http.MethodPut {
					var payload map[string]string
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					assert.Equal(t, map[string]string{"data": "secret", "description": "deploy key"}, payload)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			service := ActionSecrets{Client: client, Scope: scope.scope}
			item, err := service.Set(context.Background(), "TOKEN", "secret", "deploy key")
			require.NoError(t, err)
			require.Equal(t, ActionSecret{Name: "TOKEN", Description: "deploy key"}, item)
			require.NoError(t, service.Delete(context.Background(), "A B"))
			require.Equal(t, []string{"PUT " + scope.path + "/secrets/TOKEN", "DELETE " + scope.path + "/secrets/A%20B"}, requests)
		})
	}
}

func TestSecretListReturnsMetadataOnly(t *testing.T) {
	for _, scope := range actionValueScopes {
		t.Run(scope.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, scope.path+"/secrets", r.URL.Path)
				assert.Equal(t, "20", r.URL.Query().Get("limit"))
				assert.Equal(t, "2", r.URL.Query().Get("page"))
				_, _ = w.Write([]byte(`[{"name":"TOKEN","data":"never-display","description":"deploy","created_at":"2028-01-02T03:04:05Z"},{"name":"OTHER"}]`))
			}))
			items, err := (ActionSecrets{Client: client, Scope: scope.scope}).ListPage(context.Background(), 20, 2)
			if scope.scope.Account {
				var unsupported *CapabilityError
				require.ErrorAs(t, err, &unsupported)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []ActionSecret{{Name: "TOKEN", Description: "deploy", CreatedAt: "2028-01-02T03:04:05Z"}, {Name: "OTHER"}}, items.Items)
		})
	}
}

func TestVariableSetKeepsTheDescriptionUnlessGiven(t *testing.T) {
	for _, test := range []struct {
		name        string
		description *string
		want        string
	}{{"omitted", nil, "build flavor"}, {"given", new("other"), "other"}, {"cleared", new(""), ""}} {
		t.Run(test.name, func(t *testing.T) {
			var sent string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"name":"MODE","data":"old","description":"build flavor"}`))
					return
				}
				var payload map[string]string
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				sent = payload["description"]
				w.WriteHeader(http.StatusNoContent)
			}))
			item, err := (ActionVariables{Client: client, Scope: ActionScope{Organization: "team"}}).Set(context.Background(), "MODE", "new", test.description)
			require.NoError(t, err)
			require.Equal(t, test.want, sent)
			require.Equal(t, test.want, item.Description)
		})
	}
}

func TestMissingScopeIsNotReportedAsAMissingValue(t *testing.T) {
	for _, test := range []struct {
		message string
		missing bool
	}{{"not found", false}, {"secret was not found [name: ]", true}, {"variable not found", true}} {
		t.Run(test.message, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"` + test.message + `"}`))
			}))
			scope := ActionScope{Organization: "acme"}
			for name, err := range map[string]error{
				"secret":   (ActionSecrets{Client: client, Scope: scope}).Delete(context.Background(), "TOKEN"),
				"variable": (ActionVariables{Client: client, Scope: scope}).Delete(context.Background(), "MODE"),
			} {
				var missing *NotFoundError
				require.Error(t, err)
				if strings.Contains(test.message, name) {
					require.ErrorAs(t, err, &missing, name)
				} else {
					require.False(t, errors.As(err, &missing), name)
				}
			}
		})
	}
}

func TestSecretSetResultHasEveryJSONField(t *testing.T) {
	data, err := json.Marshal(ActionSecret{Name: "TOKEN"})
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"TOKEN","description":"","created_at":""}`, string(data))
}
