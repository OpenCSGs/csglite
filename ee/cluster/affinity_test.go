// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"testing"
	"time"
)

func TestAffinityExpires(t *testing.T) {
	a := NewAffinity()
	now := time.Now()
	a.now = func() time.Time { return now }
	a.Record("k", "n1")
	if got, ok := a.Lookup("k"); !ok || got != "n1" {
		t.Fatal("lookup failed")
	}
	now = now.Add(affinityTTL + time.Second)
	if _, ok := a.Lookup("k"); ok {
		t.Fatal("entry did not expire")
	}
	ctx := WithAffinityKey(context.Background(), "thread:x")
	if AffinityKeyFromContext(ctx) != "thread:x" || AffinityKeyFromContext(context.Background()) != "" {
		t.Fatal("context round trip")
	}
}
