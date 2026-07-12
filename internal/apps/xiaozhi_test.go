package apps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opencsgs/csglite/internal/config"
)

func TestXiaozhiAvailabilityExplainsMissingPrerequisite(t *testing.T) {
	t.Setenv("PATH", "")
	got := xiaozhiDockerAvailability(context.Background())
	if !xiaozhiHostCanRunAMD64() {
		if got.reason != "architecture_unsupported" || got.supported {
			t.Fatalf("availability = %#v, want unsupported architecture", got)
		}
		return
	}
	if got.reason != "docker_not_found" || !got.supported {
		t.Fatalf("availability = %#v, want supported platform with missing Docker", got)
	}
}

func TestXiaozhiRootUsesConfiguredStorage(t *testing.T) {
	storage := t.TempDir()
	mgr := &Manager{cfg: &config.Config{
		ModelDir:   filepath.Join(storage, config.ModelsDir),
		DatasetDir: filepath.Join(storage, config.DatasetsDir),
	}}
	got, err := mgr.xiaozhiRoot()
	if err != nil {
		t.Fatalf("xiaozhiRoot: %v", err)
	}
	want := filepath.Join(storage, "apps", "xiaozhi")
	if got != want {
		t.Fatalf("xiaozhiRoot = %q, want %q", got, want)
	}
}

func TestPrepareXiaozhiFilesKeepsSecretsPrivate(t *testing.T) {
	storage := t.TempDir()
	mgr := &Manager{cfg: &config.Config{
		ModelDir:   filepath.Join(storage, config.ModelsDir),
		DatasetDir: filepath.Join(storage, config.DatasetsDir),
	}}
	if err := mgr.prepareXiaozhiFiles(); err != nil {
		t.Fatalf("prepareXiaozhiFiles: %v", err)
	}
	root, _ := mgr.xiaozhiRoot()
	for _, name := range []string{".env", "compose.yml", filepath.Join("config", "private.key")} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	env, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), "xiaozhi_passwd") {
		t.Fatal("environment uses the deployment guide's default password")
	}
	for _, expected := range []string{
		"XIAOZHI_ADMIN_EMAIL=csglite@opencsg.com",
		"XIAOZHI_ADMIN_PASSWORD=csglite",
	} {
		if !strings.Contains(string(env), expected) {
			t.Fatalf("environment missing %q", expected)
		}
	}
	privateKey, err := os.ReadFile(filepath.Join(root, "config", "private.key"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(privateKey), "-----BEGIN EC PRIVATE KEY-----") {
		t.Fatal("generated private key is not an EC PEM key")
	}
	if mgr.xiaozhiInstalled() {
		t.Fatal("prepared files must not count as installed before image pull succeeds")
	}
	marker, _ := mgr.xiaozhiInstallMarkerPath()
	if err := os.WriteFile(marker, []byte(xiaozhiVersion+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mgr.xiaozhiInstalled() {
		t.Fatal("compose file plus successful install marker should count as installed")
	}
}

func TestXiaozhiComposeUsesHardenedManagedLayout(t *testing.T) {
	for _, unwanted := range []string{
		"POSTGRES_HOST_AUTH_METHOD",
		"container_name:",
		"1025:1025",
		"8025:8025",
		"9308:9308",
		":latest",
	} {
		if strings.Contains(xiaozhiComposeYAML, unwanted) {
			t.Fatalf("compose template contains unsafe or mutable value %q", unwanted)
		}
	}
	for _, wanted := range []string{
		"./postgres:/var/lib/postgresql/data",
		"./redis:/data",
		"host.docker.internal:host-gateway",
		"platform: linux/amd64",
		`done < /proc/net/tcp; exit 1`,
		"127.0.0.1:${FRONTEND_PORT}:8080",
		"xiaozhi_admin_init:",
		`endpoint="http://127.0.0.1:3011/api/setup/create-admin-user"`,
		"DEPLOYMENT_TYPE: selfhosted",
		"db.$$disconnect()",
		`workspaceUserRole.count({where:{userId:user.id,status:"Accepted"}})`,
		"db.$$transaction(async tx=>",
		`type:99,status:"Accepted"`,
	} {
		if !strings.Contains(xiaozhiComposeYAML, wanted) {
			t.Fatalf("compose template missing %q", wanted)
		}
	}
}

func TestPrepareXiaozhiFilesPreservesCustomAdminCredentials(t *testing.T) {
	storage := t.TempDir()
	mgr := &Manager{cfg: &config.Config{
		ModelDir:   filepath.Join(storage, config.ModelsDir),
		DatasetDir: filepath.Join(storage, config.DatasetsDir),
	}}
	root, _ := mgr.xiaozhiRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("XIAOZHI_ADMIN_PASSWORD=custom-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := mgr.prepareXiaozhiFiles(); err != nil {
		t.Fatalf("prepareXiaozhiFiles: %v", err)
	}
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(env)
	if !strings.Contains(content, "XIAOZHI_ADMIN_PASSWORD=custom-password\n") {
		t.Fatal("custom admin password was overwritten")
	}
	if strings.Contains(content, "XIAOZHI_ADMIN_PASSWORD=csglite\n") {
		t.Fatal("default admin password was appended over a custom value")
	}
	if !strings.Contains(content, "XIAOZHI_ADMIN_EMAIL=csglite@opencsg.com\n") {
		t.Fatal("missing admin email default was not appended")
	}
}

func TestPrepareXiaozhiFilesMigratesPreviousBuiltInPassword(t *testing.T) {
	storage := t.TempDir()
	mgr := &Manager{cfg: &config.Config{
		ModelDir:   filepath.Join(storage, config.ModelsDir),
		DatasetDir: filepath.Join(storage, config.DatasetsDir),
	}}
	root, _ := mgr.xiaozhiRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte(
		"XIAOZHI_ADMIN_EMAIL=admin@csghub-lite.local\n"+
			"XIAOZHI_ADMIN_USERNAME=csglite\n"+
			"XIAOZHI_ADMIN_PASSWORD=csghub-lite\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := mgr.prepareXiaozhiFiles(); err != nil {
		t.Fatalf("prepareXiaozhiFiles: %v", err)
	}
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(env)
	if strings.Contains(content, "XIAOZHI_ADMIN_PASSWORD=csghub-lite") {
		t.Fatal("previous built-in password was not migrated")
	}
	if !strings.Contains(content, "XIAOZHI_ADMIN_PASSWORD=csglite\n") {
		t.Fatal("new built-in password is missing")
	}
	if strings.Contains(content, "XIAOZHI_ADMIN_EMAIL=admin@csghub-lite.local") {
		t.Fatal("previous built-in email was not migrated")
	}
	if !strings.Contains(content, "XIAOZHI_ADMIN_EMAIL=csglite@opencsg.com\n") {
		t.Fatal("new built-in email is missing")
	}
	if strings.Contains(content, "XIAOZHI_ADMIN_USERNAME=csglite") {
		t.Fatal("obsolete built-in username was not removed")
	}
}

func TestXiaozhiComposeTemplateParsesWhenDockerComposeIsAvailable(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker is not available")
	}
	storage := t.TempDir()
	mgr := &Manager{cfg: &config.Config{
		ModelDir:   filepath.Join(storage, config.ModelsDir),
		DatasetDir: filepath.Join(storage, config.DatasetsDir),
	}}
	if err := mgr.prepareXiaozhiFiles(); err != nil {
		t.Fatal(err)
	}
	root, _ := mgr.xiaozhiRoot()
	cmd := exec.Command(docker, "compose", "--file", filepath.Join(root, "compose.yml"), "config", "--quiet")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}
}
