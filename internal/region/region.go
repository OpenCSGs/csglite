package region

import (
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	EnvName = "CSGHUB_LITE_REGION"
	CN      = "CN"
	INTL    = "INTL"
)

const ipCountryURL = "https://ipinfo.io/country"

var (
	ipOnce   sync.Once
	ipRegion string
)

// Detect returns CN or INTL. CSGHUB_LITE_REGION wins when set; otherwise the
// public IP country is used, matching installer and upgrade region detection.
// IP lookup failures default to CN. Tests skip the network lookup unless the
// env override is set.
func Detect() string {
	if parsed, ok := parseEnv(os.Getenv(EnvName)); ok {
		return parsed
	}
	if testing.Testing() {
		return INTL
	}
	return detectFromIP()
}

func parseEnv(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", false
	case "cn", "china", "mainland", "domestic":
		return CN, true
	case "intl", "international", "global", "foreign", "overseas":
		return INTL, true
	default:
		return INTL, true
	}
}

func detectFromIP() string {
	ipOnce.Do(func() {
		ipRegion = lookupIPRegion(ipCountryURL)
	})
	return ipRegion
}

func lookupIPRegion(url string) string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return CN
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CN
	}
	if strings.TrimSpace(string(body)) == CN {
		return CN
	}
	return INTL
}
