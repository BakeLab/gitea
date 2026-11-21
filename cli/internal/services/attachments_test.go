// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/require"
)

type digestSigner struct{ contents, digests []string }

func (s *digestSigner) SignRequest(r *http.Request, contents []byte) error {
	s.contents = append(s.contents, string(contents))
	s.digests = append(s.digests, r.Header.Get("Digest"))
	return nil
}

func TestUploadOfAFileIsSignedWithoutBuffering(t *testing.T) {
	var digest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		sum := sha512.Sum512(data)
		digest = "SHA-512=" + base64.StdEncoding.EncodeToString(sum[:])
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		require.NoError(t, err)
		part, err := multipart.NewReader(bytes.NewReader(data), params["boundary"]).NextPart()
		require.NoError(t, err)
		require.Equal(t, "file.bin", part.FileName())
		content, err := io.ReadAll(part)
		require.NoError(t, err)
		require.Equal(t, "asset bytes", string(content), "the file is read from its offset")
		_, _ = w.Write([]byte(`{"id":2,"name":"file.bin"}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "file.bin")
	require.NoError(t, os.WriteFile(path, []byte("skip asset bytes"), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	_, err = file.Seek(int64(len("skip ")), io.SeekStart)
	require.NoError(t, err)
	signer := &digestSigner{}
	client := transport.New(server.URL, "", false)
	client.Signer = signer
	_, err = (Releases{Client: client, Repository: "alice/project"}).UploadAsset(context.Background(), 1, "file.bin", file)
	require.NoError(t, err)
	require.Equal(t, []string{""}, signer.contents, "the upload is hashed, not buffered for the signer")
	require.Equal(t, []string{digest}, signer.digests)
}

func TestAttachmentHiddenFromASignatureNamesTheCause(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client := transport.New(server.URL, "", false)
	_, err := downloadAttachment(context.Background(), client, &gitea.Attachment{UUID: "a1"})
	require.True(t, IsNotFound(err))
	client.Signer = &digestSigner{}
	_, err = downloadAttachment(context.Background(), client, &gitea.Attachment{UUID: "a1"})
	require.ErrorIs(t, err, errAttachmentSignature)
}
