// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

// CapabilityError reports an operation absent from the Gitea REST contract.
type CapabilityError struct{ Feature, Reason string }

func (e *CapabilityError) Error() string { return e.Feature + " is unsupported: " + e.Reason }
