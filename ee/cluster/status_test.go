// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"testing"
)

func TestNodeQuotaEligibilityIsIndependentOfEdition(t *testing.T) {
	for _, enterprise := range []bool{false, true} {
		for _, limit := range []int{1, 2, 0} {
			m, err := New(Options{Dir: t.TempDir(), Host: &fakeHost{licensed: enterprise, limit: limit}})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := m.store.Create("test"); err != nil {
				t.Fatal(err)
			}
			if _, err := m.store.Upsert(Member{UUID: "peer", Name: "peer", CertFingerprint: "sha256:test"}, true); err != nil {
				t.Fatal(err)
			}
			view := m.View(context.Background())
			if view.Licensed != enterprise {
				t.Fatalf("edition changed: %+v", view)
			}
			if got, want := view.Members[0].Status.Licensed, limit != 1; got != want {
				t.Fatalf("enterprise=%v limit=%d: quota eligibility=%v, want %v", enterprise, limit, got, want)
			}
		}
	}
}
