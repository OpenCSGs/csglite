package server

import (
	"testing"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/inference"
)

// Which source wins is one policy shared by chat, embeddings and speech. These
// cases pin the order so a fifth namespace cannot be added to one caller and
// forgotten in the others.
func TestRouteForSourceHonoursTheNamedSource(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		name   string
		source string
		want   sourceRoute
	}{
		{"nothing named stays local", "", routeLocalRuntime},
		{"local stays local", "local", routeLocalRuntime},
		{"cloud goes to the cloud", "cloud", routeCloud},
		{"cloud is case insensitive", "Cloud", routeCloud},
		{"a pool goes to the pool", "pool:p1", routeProviderPool},
		{"a provider goes to the provider", "provider:x", routeThirdPartyProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := s.routeForSource("m", tc.source, true)
			if err != nil {
				t.Fatalf("routeForSource(%q): %v", tc.source, err)
			}
			if got != tc.want {
				t.Fatalf("routeForSource(%q) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}

// A cluster source on a node where the feature is off is a clear 404 rather
// than a request quietly served locally.
func TestRouteForSourceRejectsClusterWhenDisabled(t *testing.T) {
	s := newTestServer(t)
	if s.cluster != nil {
		t.Skip("this server has the cluster enabled")
	}
	for _, source := range []string{cluster.SourceCluster, cluster.SourceNodePrefix + "abc"} {
		if _, _, err := s.routeForSource("m", source, true); inference.HTTPStatusCode(err) != 404 {
			t.Fatalf("routeForSource(%q) = %v, want 404", source, err)
		}
	}
}

// A kind of request the cluster does not route must say so instead of silently
// ignoring the source the caller asked for.
func TestRouteForSourceRefusesClusterForKindsThatDoNotRoute(t *testing.T) {
	s := newTestServer(t)
	if _, _, err := s.routeForSource("m", cluster.SourceCluster, false); inference.HTTPStatusCode(err) != 501 {
		t.Fatalf("err = %v, want 501", err)
	}
}

// Insisting on this machine means a failed local load is the answer, not a
// silent hop to a member that happens to hold the model.
func TestClusterIsNotASubstituteWhenLocalWasDemanded(t *testing.T) {
	s := newTestServer(t)
	if s.clusterIsTheClosestSubstitute("m", "local") {
		t.Fatal("a source of local still fell back to the cluster")
	}
	if s.clusterIsTheClosestSubstitute("m", "LOCAL") {
		t.Fatal("the local check is case sensitive")
	}
}
