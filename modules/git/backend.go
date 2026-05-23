// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"fmt"
	"sort"
	"sync"
)

var backendRegistry = struct {
	sync.RWMutex
	current  string
	backends map[string]Backend
}{
	backends: make(map[string]Backend),
}

func init() {
	mustRegisterBackend(cliBackend{})
	mustUseBackend("cli")
}

// RegisterBackend makes a git backend available for later switching.
func RegisterBackend(backend Backend) error {
	if backend == nil {
		return fmt.Errorf("git backend is nil")
	}
	name := backend.Name()
	if name == "" {
		return fmt.Errorf("git backend name is empty")
	}

	backendRegistry.Lock()
	defer backendRegistry.Unlock()
	backendRegistry.backends[name] = backend
	if backendRegistry.current == "" {
		backendRegistry.current = name
	}
	return nil
}

func mustRegisterBackend(backend Backend) {
	if err := RegisterBackend(backend); err != nil {
		panic(err)
	}
}

// UseBackend switches the active git backend implementation.
func UseBackend(name string) error {
	backendRegistry.Lock()
	defer backendRegistry.Unlock()
	if _, ok := backendRegistry.backends[name]; !ok {
		return fmt.Errorf("git backend %q is not registered", name)
	}
	backendRegistry.current = name
	return nil
}

func mustUseBackend(name string) {
	if err := UseBackend(name); err != nil {
		panic(err)
	}
}

// CurrentBackendName returns the active backend name.
func CurrentBackendName() string {
	backendRegistry.RLock()
	defer backendRegistry.RUnlock()
	return backendRegistry.current
}

// RegisteredBackends returns all registered backend names.
func RegisteredBackends() []string {
	backendRegistry.RLock()
	defer backendRegistry.RUnlock()
	out := make([]string, 0, len(backendRegistry.backends))
	for name := range backendRegistry.backends {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func currentBackend() Backend {
	backendRegistry.RLock()
	defer backendRegistry.RUnlock()
	return backendRegistry.backends[backendRegistry.current]
}

func setBackendForTesting(t testingT, backend Backend) {
	t.Helper()
	prev := CurrentBackendName()
	mustRegisterBackend(backend)
	mustUseBackend(backend.Name())
	t.Cleanup(func() { mustUseBackend(prev) })
}

type testingT interface {
	Cleanup(func())
	Helper()
}
