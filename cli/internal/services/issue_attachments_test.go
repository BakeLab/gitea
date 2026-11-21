// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attachmentOps are the attachment operations of an issue or a comment.
type attachmentOps struct {
	list     func(ctx context.Context, limit, page int) (Page[IssueAttachment], error)
	get      func(ctx context.Context, id int64) (IssueAttachment, error)
	rename   func(ctx context.Context, id int64, name string) (IssueAttachment, error)
	remove   func(ctx context.Context, id int64) error
	download func(ctx context.Context, id int64) (IssueAttachment, func() (io.ReadCloser, error), error)
}

func TestIssueAndCommentAttachmentsUseTheirEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, base, missing string
		ops                 func(Issues, Comments) attachmentOps
	}{
		{
			name: "issue", base: "/api/v1/repos/o/r/issues/7/assets", missing: "attachment 4 not found in o/r#7",
			ops: func(issues Issues, _ Comments) attachmentOps {
				return attachmentOps{
					list: func(ctx context.Context, limit, page int) (Page[IssueAttachment], error) {
						return issues.Attachments(ctx, 7, limit, page)
					},
					get: func(ctx context.Context, id int64) (IssueAttachment, error) { return issues.Attachment(ctx, 7, id) },
					rename: func(ctx context.Context, id int64, name string) (IssueAttachment, error) {
						return issues.RenameAttachment(ctx, 7, id, name)
					},
					remove: func(ctx context.Context, id int64) error { return issues.DeleteAttachment(ctx, 7, id) },
					download: func(ctx context.Context, id int64) (IssueAttachment, func() (io.ReadCloser, error), error) {
						return issues.DownloadAttachment(ctx, 7, id)
					},
				}
			},
		},
		{
			name: "comment", base: "/api/v1/repos/o/r/issues/comments/5/assets", missing: "attachment 4 not found in comment 5 on o/r",
			ops: func(_ Issues, comments Comments) attachmentOps {
				return attachmentOps{
					list: func(ctx context.Context, limit, page int) (Page[IssueAttachment], error) {
						return comments.Attachments(ctx, 5, limit, page)
					},
					get: func(ctx context.Context, id int64) (IssueAttachment, error) { return comments.Attachment(ctx, 5, id) },
					rename: func(ctx context.Context, id int64, name string) (IssueAttachment, error) {
						return comments.RenameAttachment(ctx, 5, id, name)
					},
					remove: func(ctx context.Context, id int64) error { return comments.DeleteAttachment(ctx, 5, id) },
					download: func(ctx context.Context, id int64) (IssueAttachment, func() (io.ReadCloser, error), error) {
						return comments.DownloadAttachment(ctx, 5, id)
					},
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const attachment = `{"id":3,"name":"a.txt","size":5,"uuid":"u-3","download_count":2,"browser_download_url":"https://gitea.test/attachments/u-3","created_at":"2026-10-01T12:00:00Z"}`
			var mutations []string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET " + tc.base:
					_, _ = w.Write([]byte(`[` + attachment + `,{"id":8,"name":"b.txt"}]`))
				case "GET " + tc.base + "/3":
					_, _ = w.Write([]byte(attachment))
				case "GET /attachments/u-3":
					_, _ = w.Write([]byte("hello"))
				case "PATCH " + tc.base + "/3":
					assert.Equal(t, map[string]any{"name": "c.txt"}, requestJSON(t, r))
					mutations = append(mutations, "rename")
					_, _ = w.Write([]byte(`{"id":3,"name":"c.txt"}`))
				case "DELETE " + tc.base + "/3":
					mutations = append(mutations, "delete")
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			ops := tc.ops(Issues{Client: client, Repository: "o/r"}, Comments{Client: client, Repository: "o/r"})
			ctx := context.Background()
			want := IssueAttachment{ID: 3, Name: "a.txt", Size: 5, URL: "https://gitea.test/attachments/u-3", CreatedAt: "2026-10-01T12:00:00Z", DownloadCount: 2}

			page, err := ops.list(ctx, 1, 1)
			require.NoError(t, err)
			require.Equal(t, Page[IssueAttachment]{Items: []IssueAttachment{want}, More: true, Total: 2, Counted: true}, page)
			got, err := ops.get(ctx, 3)
			require.NoError(t, err)
			require.Equal(t, want, got)
			renamed, err := ops.rename(ctx, 3, "c.txt")
			require.NoError(t, err)
			require.Equal(t, "c.txt", renamed.Name)
			require.NoError(t, ops.remove(ctx, 3))
			require.Equal(t, []string{"rename", "delete"}, mutations)

			got, open, err := ops.download(ctx, 3)
			require.NoError(t, err)
			require.Equal(t, want, got)
			body, err := open()
			require.NoError(t, err)
			content, err := io.ReadAll(body)
			require.NoError(t, err)
			require.NoError(t, body.Close())
			require.Equal(t, "hello", string(content))

			_, err = ops.get(ctx, 4)
			require.EqualError(t, err, tc.missing)
			_, err = ops.rename(ctx, 4, "x")
			require.EqualError(t, err, tc.missing)
			require.EqualError(t, ops.remove(ctx, 4), tc.missing)
			_, _, err = ops.download(ctx, 4)
			require.EqualError(t, err, tc.missing)
		})
	}
}
