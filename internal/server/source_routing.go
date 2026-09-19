package server

import (
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/inference"
)

// A request names where it wants to run in its "source" field, and that
// vocabulary has grown to five namespaces owned by three packages: "local",
// "cloud" and the empty string here, "pool:<id>" and "provider:<id>" next door,
// and "cluster" and "node:<uuid>" in the cluster package. Which one wins is a
// single policy, but it used to be written out separately for chat, for
// embeddings, for speech recognition and for speech synthesis. Four copies mean
// four places to forget something, and one of them did: nothing taught the AI
// app route vocabulary about the cluster, so an app could not be pinned to it.
//
// routeForSource is the one place that answers "where does this request go",
// and each caller then builds the backend its own kind of request needs.
type sourceRoute int

const (
	// routeLocalRuntime means the request named nothing that sends it away, so
	// the caller loads the model here and falls back its own way if that fails.
	routeLocalRuntime sourceRoute = iota
	routeProviderPool
	routeThirdPartyProvider
	routeCloud
	routeCluster
)

// routeForSource decides where a request goes from the source it named. For
// routeCluster it also returns the cluster source to use, which is the one the
// request named when it named one, and plain "cluster" when the request named
// nothing but the cluster wants this model anyway.
//
// wantsCluster is false for kinds of request the cluster does not route; those
// callers never reach routeCluster.
func (s *Server) routeForSource(modelID, source string, wantsCluster bool) (sourceRoute, string, error) {
	if poolIDFromSource(source) != "" {
		return routeProviderPool, "", nil
	}
	if providerIDFromSource(source) != "" {
		return routeThirdPartyProvider, "", nil
	}
	if strings.EqualFold(strings.TrimSpace(source), "cloud") {
		return routeCloud, "", nil
	}
	if cluster.IsClusterSource(source) {
		if !wantsCluster {
			return 0, "", inference.NewHTTPStatusError(http.StatusNotImplemented, "the cluster does not route this kind of request")
		}
		if s.cluster == nil {
			return 0, "", inference.NewHTTPStatusError(http.StatusNotFound, "the cluster feature is disabled on this node")
		}
		return routeCluster, source, nil
	}
	// Nothing named: the LAN cluster still takes the request when a peer holds
	// a model this node lacks, or for every model in balanced mode. A model
	// present here in local-first mode falls through to the local runtime.
	if wantsCluster && strings.TrimSpace(source) == "" && s.clusterRoutingWanted(modelID) {
		return routeCluster, cluster.SourceCluster, nil
	}
	return routeLocalRuntime, "", nil
}

// clusterIsTheClosestSubstitute reports whether a failed local load should be
// answered by a member that holds the model. It is the same question wherever a
// local load fails, and a source of "local" means the caller insisted on this
// machine.
func (s *Server) clusterIsTheClosestSubstitute(modelID, source string) bool {
	if strings.EqualFold(strings.TrimSpace(source), "local") {
		return false
	}
	return s.cluster != nil && s.cluster.RemoteHasModel(modelID)
}
