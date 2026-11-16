package cli

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

func publicMasterOrigin(value string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("public Master address must be an HTTPS origin without credentials, path, query or fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func clusterJoinGateway(gateway, server, address string) (string, error) {
	var target string
	for _, value := range []string{gateway, server} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		origin, err := publicMasterOrigin(value)
		if err != nil {
			return "", err
		}
		if target != "" && origin != target {
			return "", errors.New("--gateway and --server must name the same Master")
		}
		target = origin
	}
	if target != "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(address)), "https://") {
		if strings.TrimSpace(address) != "" {
			origin, err := publicMasterOrigin(address)
			if err != nil || (target != "" && origin != target) {
				return "", errors.New("--address must match the public Master address")
			}
			target = origin
		}
	}
	return target, nil
}

func publicWorkerJoinConfig(current *config.Config, gateway, caFile string, caSet bool) (*config.Config, error) {
	if current.Cluster.Secret != "" {
		return nil, errors.New("unset cluster_secret before joining a public Master")
	}
	next := *current
	next.Cluster.Mode, next.Cluster.GatewayURL = "worker", gateway
	if caSet {
		next.Cluster.TLSCAFile = ""
		if caFile != "" {
			absolute, err := filepath.Abs(caFile)
			if err != nil {
				return nil, fmt.Errorf("resolve CA path: %w", err)
			}
			next.Cluster.TLSCAFile = absolute
		}
	}
	if next.Cluster.TLSCAFile != "" {
		pem, err := os.ReadFile(next.Cluster.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read Master CA file: %w", err)
		}
		if !x509.NewCertPool().AppendCertsFromPEM(pem) {
			return nil, errors.New("Master CA file contains no certificates")
		}
	}
	if next.ListenAddr == "" || next.ListenAddr == config.DefaultListenAddr {
		next.ListenAddr = "127.0.0.1:11435"
	}
	host, _, err := net.SplitHostPort(next.ListenAddr)
	if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
		return nil, errors.New("public worker management must bind loopback; set listen_addr to 127.0.0.1:11435 first")
	}
	return &next, nil
}

func validatePublicJoinMembership(info *cluster.ClusterInfo, mode, configuredGateway, gateway, clusterID string) error {
	if info != nil && (info.Topology != "public" || mode != "worker" || strings.TrimRight(configuredGateway, "/") != gateway || info.UUID != clusterID) {
		return errors.New("this node already belongs to a cluster; leave it before changing Master or topology")
	}
	return nil
}

func joinPublicWorker(cmd *cobra.Command, token, gateway, caFile string, caSet bool) error {
	clusterID, _, err := cluster.ParseJoinToken(token)
	if err != nil {
		return err
	}
	if startupToken := strings.TrimSpace(os.Getenv("CSGHUB_LITE_CLUSTER_JOIN_TOKEN")); startupToken != "" && startupToken != token {
		return errors.New("unset CSGHUB_LITE_CLUSTER_JOIN_TOKEN before joining with a different token")
	}
	current, err := config.Load()
	if err != nil {
		return err
	}
	next, err := publicWorkerJoinConfig(current, gateway, caFile, caSet)
	if err != nil {
		return err
	}
	dir := filepath.Join(current.StorageDir(), "cluster")
	store, err := cluster.OpenStore(dir)
	if err != nil {
		return fmt.Errorf("read cluster membership: %w", err)
	}
	if err := validatePublicJoinMembership(store.Cluster(), current.Cluster.Mode, current.Cluster.GatewayURL, gateway, clusterID); err != nil {
		return err
	}
	wasRunning := serverHealthy(serverBaseURL(current))
	var live cluster.ClusterView
	if wasRunning {
		client := &clusterClient{baseURL: serverBaseURL(current)}
		if err := client.do(http.MethodGet, "/api/cluster", nil, &live); err != nil {
			return fmt.Errorf("read running cluster state: %w", err)
		}
		if err := validatePublicJoinMembership(live.Cluster, live.Mode, live.GatewayURL, gateway, clusterID); err != nil {
			return err
		}
	}
	changed := next.Cluster != current.Cluster || next.ListenAddr != current.ListenAddr
	if changed {
		if err := config.Save(next); err != nil {
			return fmt.Errorf("save worker configuration: %w", err)
		}
	}
	needsRestart := wasRunning && (changed || live.Mode != "worker" || strings.TrimRight(live.GatewayURL, "/") != gateway)
	apply := func() error {
		if needsRestart {
			return restartBackgroundService(next)
		}
		return startBackgroundService(next)
	}
	join := func() error {
		if err := apply(); err != nil {
			return err
		}
		client := &clusterClient{baseURL: serverBaseURL(next)}
		var view cluster.ClusterView
		if err := client.do(http.MethodGet, "/api/cluster", nil, &view); err != nil {
			return err
		}
		if view.Mode != "worker" || strings.TrimRight(view.GatewayURL, "/") != gateway {
			return errors.New("running Lite did not apply the requested worker configuration")
		}
		if err := validatePublicJoinMembership(view.Cluster, view.Mode, view.GatewayURL, gateway, clusterID); err != nil {
			return err
		}
		if view.Cluster != nil {
			cmd.Printf("Already joined cluster %q (%s).\n", view.Cluster.Name, view.Cluster.UUID)
			return nil
		}
		if err := client.do(http.MethodPost, "/api/cluster/join", map[string]string{"token": token, "address": gateway}, &view); err != nil {
			return err
		}
		if view.Cluster == nil || view.Cluster.UUID != clusterID {
			return errors.New("local service returned an invalid join receipt")
		}
		cmd.Printf("Joined cluster %q (%s) with %d member(s).\n", view.Cluster.Name, view.Cluster.UUID, len(view.Members))
		return nil
	}
	if err := join(); err != nil {
		// A lost HTTP response must never undo a successfully persisted pairing.
		latest, readErr := cluster.OpenStore(dir)
		if readErr != nil || latest.InCluster() {
			return fmt.Errorf("join confirmation failed; worker configuration retained, check 'csghub-lite cluster status': %w", err)
		}
		if changed || needsRestart || !wasRunning {
			restoreErr := stopBackgroundServiceForRestart()
			if changed {
				restoreErr = errors.Join(restoreErr, config.Save(current))
			}
			if wasRunning && restoreErr == nil {
				restoreErr = startBackgroundService(current)
			}
			if restoreErr != nil {
				return errors.Join(err, fmt.Errorf("restore previous worker configuration: %w", restoreErr))
			}
		}
		return err
	}
	return nil
}
