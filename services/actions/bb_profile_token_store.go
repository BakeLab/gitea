// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Gitea's BB reconciliation is protected by the database-wide
// actions_bb_executor lock. This mutex also serializes token refreshes within
// one process while the SDK holds its store transaction across refresh/save.
type bbProfileTokenStore struct {
	path string
	mu   sync.Mutex
}

type bbStoredTokenFile struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at"`
}

func (s *bbProfileTokenStore) WithLock(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

type bbStoredToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func (s *bbProfileTokenStore) Load(ctx context.Context) (bbStoredToken, error) {
	var token bbStoredToken
	if err := ctx.Err(); err != nil {
		return token, err
	}
	file, err := openBBTokenFile(s.path)
	if err != nil {
		return token, err
	}
	defer file.Close()
	var record bbStoredTokenFile
	decoder := json.NewDecoder(io.LimitReader(file, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return token, errors.New("invalid BB profile token file")
	}
	if record.AccessToken == "" || record.RefreshToken == "" || record.ExpiresAt <= 0 {
		return token, errors.New("BB profile token file is incomplete")
	}
	return bbStoredToken{AccessToken: record.AccessToken, RefreshToken: record.RefreshToken, ExpiresAt: time.Unix(record.ExpiresAt, 0)}, nil
}

func (s *bbProfileTokenStore) Save(ctx context.Context, token bbStoredToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresAt.IsZero() {
		return errors.New("refusing to save incomplete BB profile credentials")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o077 != 0 {
		return errors.New("BB profile credential directory must be private")
	}
	tmp, err := os.CreateTemp(dir, ".bb-profile-token-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	data, err := json.Marshal(bbStoredTokenFile{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: token.ExpiresAt.Unix()})
	if err == nil {
		_, err = tmp.Write(append(data, '\n'))
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	if directory, openErr := os.Open(dir); openErr == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func openBBTokenFile(path string) (*os.File, error) {
	dirInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || dirInfo.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("BB profile credential directory must be a private directory")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 64*1024 {
		return nil, errors.New("BB profile token file must be a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, errors.New("BB profile token file changed while opening")
	}
	return file, nil
}
