package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ThirdPartyProvider represents a third-party API provider configuration
type ThirdPartyProvider struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	BaseURL  string           `json:"base_url"`
	APIKey   string           `json:"api_key"`
	Provider string           `json:"provider,omitempty"`
	Enabled  bool             `json:"enabled"`
	Headers  []ProviderHeader `json:"headers,omitempty"`
}

type ProviderHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ProvidersFile is the filename for storing third-party providers
const ProvidersFile = "providers.json"

var (
	providers     []ThirdPartyProvider
	providersOnce sync.Once
	providersMu   sync.RWMutex
)

// ProvidersPath returns the path to the providers config file
func ProvidersPath() (string, error) {
	home, err := AppHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ProvidersFile), nil
}

// LoadProviders loads third-party providers from the config file
func LoadProviders() ([]ThirdPartyProvider, error) {
	var loadErr error
	providersOnce.Do(func() {
		providers = []ThirdPartyProvider{}

		cfgPath, err := ProvidersPath()
		if err != nil {
			loadErr = err
			return
		}

		data, err := os.ReadFile(cfgPath)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			loadErr = err
			return
		}

		var loaded []ThirdPartyProvider
		if err := json.Unmarshal(data, &loaded); err != nil {
			loadErr = err
			return
		}
		providers = loaded
	})
	return providers, loadErr
}

// GetProviders returns the loaded third-party providers
func GetProviders() []ThirdPartyProvider {
	providersMu.RLock()
	defer providersMu.RUnlock()
	if providers == nil {
		provs, _ := LoadProviders()
		return provs
	}
	return providers
}

// SaveProviders saves the providers to the config file
func SaveProviders(provs []ThirdPartyProvider) error {
	providersMu.Lock()
	defer providersMu.Unlock()

	cfgPath, err := ProvidersPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(provs, "", "  ")
	if err != nil {
		return err
	}

	providers = provs
	return os.WriteFile(cfgPath, data, 0o600) // Use 0600 for security (contains API keys)
}

// GenerateProviderID generates a random ID for a new provider
func GenerateProviderID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ResetProviders resets the providers state (for testing)
func ResetProviders() {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers = nil
	providersOnce = sync.Once{}
}
