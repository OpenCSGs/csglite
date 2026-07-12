package apps

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/pkg/api"
)

const (
	xiaozhiAppID       = "xiaozhi"
	xiaozhiProjectName = "csghub-lite-xiaozhi"
	xiaozhiVersion     = "20260718"
	xiaozhiPort        = 8080
)

var xiaozhiComposeYAML = `name: csghub_lite_xiaozhi
services:
  postgres:
    image: opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi@sha256:6f2fedef8e4311682b3a5989a21bf527d3310ab5421258ad6e41e52955c16294
    platform: linux/amd64
    environment:
      POSTGRES_USER: ${DB_USERNAME}
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_DB: ${DB_DATABASE}
      POSTGRES_INITDB_ARGS: --data-checksums
    volumes:
      - ./postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${DB_USERNAME} -d ${DB_DATABASE}"]
      interval: 10s
      timeout: 5s
      retries: 10
    restart: unless-stopped
    networks: [xiaozhi_net]

  redis:
    image: opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi@sha256:072e0de5527bd9803096e455c41e80107fcc42292cedcc9e4f7cffd9b9e972c3
    platform: linux/amd64
    command: ["redis-server", "--appendonly", "yes"]
    volumes:
      - ./redis:/data
    healthcheck:
      test: ["CMD", "redis-cli", "--raw", "incr", "ping"]
      interval: 10s
      timeout: 5s
      retries: 10
    restart: unless-stopped
    networks: [xiaozhi_net]

  xiaozhi_migration:
    image: opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi@sha256:34497226d6c1512ec8458ff909a71b00f4920d1573b8c89cab912a1fa00bd79f
    platform: linux/amd64
    command: ["sh", "-c", "node ./scripts/self-host-predeploy.js"]
    env_file: [.env]
    environment:
      REDIS_SERVER_HOST: redis
      DATABASE_URL: postgresql://${DB_USERNAME}:${DB_PASSWORD}@postgres:5432/${DB_DATABASE}
      XIAOZHI_INDEXER_ENABLED: "false"
      DEPLOYMENT_TYPE: xiaozhi
    volumes:
      - ./storage:/root/.xiaozhi/storage
      - ./config:/root/.xiaozhi/config
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    networks: [xiaozhi_net]

  xiaozhi_backend:
    image: opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi@sha256:34497226d6c1512ec8458ff909a71b00f4920d1573b8c89cab912a1fa00bd79f
    platform: linux/amd64
    env_file: [.env]
    environment:
      REDIS_SERVER_HOST: redis
      DATABASE_URL: postgresql://${DB_USERNAME}:${DB_PASSWORD}@postgres:5432/${DB_DATABASE}
      XIAOZHI_INDEXER_ENABLED: "false"
      DEPLOYMENT_TYPE: xiaozhi
    extra_hosts:
      - host.docker.internal:host-gateway
    volumes:
      - ./storage:/root/.xiaozhi/storage
      - ./config:/root/.xiaozhi/config
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      xiaozhi_admin_init:
        condition: service_completed_successfully
    healthcheck:
      test: ["CMD", "node", "-e", "fetch('http://127.0.0.1:3010').then(()=>process.exit(0)).catch(()=>process.exit(1))"]
      interval: 10s
      timeout: 5s
      retries: 12
    restart: unless-stopped
    networks: [xiaozhi_net]

  xiaozhi_admin_init:
    image: opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi@sha256:34497226d6c1512ec8458ff909a71b00f4920d1573b8c89cab912a1fa00bd79f
    platform: linux/amd64
    command:
      - sh
      - -c
      - |
        node ./dist/main.js &
        server_pid=$$!
        node -e 'const {PrismaClient}=require("@prisma/client"); const {hash}=require("@node-rs/argon2"); const endpoint="http://127.0.0.1:3011/api/setup/create-admin-user"; const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms)); (async()=>{ const email=process.env.XIAOZHI_ADMIN_EMAIL; const password=process.env.XIAOZHI_ADMIN_PASSWORD; const setupPassword=password.length>=8?password:password.padEnd(8,"0"); let ready=false; for(let attempt=0; attempt<60; attempt++){ try { const response=await fetch(endpoint,{method:"POST",headers:{"content-type":"application/json"},body:JSON.stringify({email,password:setupPassword})}); const body=await response.text(); if(response.ok || (response.status===403 && body.includes("First user already created"))){ ready=true; break; } if(response.status!==404 && response.status!==503) throw new Error("setup endpoint returned "+response.status+": "+body); } catch(error){ if(attempt===59) throw error; } await sleep(1000); } if(!ready) throw new Error("Xiaozhi admin initialization timed out"); const db=new PrismaClient(); try { let user=await db.user.findUnique({where:{email}}); if(!user){ const feature=await db.userFeature.findFirst({where:{name:"administrator",activated:true},include:{user:true}}); user=feature&&feature.user; } if(!user) throw new Error("Xiaozhi administrator account was not found after setup"); await db.user.update({where:{id:user.id},data:{email,employeeID:null,password:await hash(password)}}); const memberships=await db.workspaceUserRole.count({where:{userId:user.id,status:"Accepted"}}); if(memberships===0){ await db.$$transaction(async tx=>{ const workspace=await tx.workspace.create({data:{public:false,name:"Xiaozhi"}}); await tx.workspaceUserRole.create({data:{workspaceId:workspace.id,userId:user.id,type:99,status:"Accepted"}}); }); } } finally { await db.$$disconnect(); } })().catch(error=>{ console.error("Xiaozhi admin initialization failed:",error.message); process.exit(1); });'
        result=$$?
        kill "$$server_pid" 2>/dev/null || true
        wait "$$server_pid" 2>/dev/null || true
        exit "$$result"
    env_file: [.env]
    environment:
      REDIS_SERVER_HOST: redis
      DATABASE_URL: postgresql://${DB_USERNAME}:${DB_PASSWORD}@postgres:5432/${DB_DATABASE}
      XIAOZHI_INDEXER_ENABLED: "false"
      DEPLOYMENT_TYPE: selfhosted
      XIAOZHI_SERVER_PORT: "3011"
    volumes:
      - ./storage:/root/.xiaozhi/storage
      - ./config:/root/.xiaozhi/config
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      xiaozhi_migration:
        condition: service_completed_successfully
    networks: [xiaozhi_net]

  xiaozhi_frontend:
    image: opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi@sha256:c94497f6ef07ff7dc7e94008561ef7366fdea5960a17154f231e38b0202708c3
    platform: linux/amd64
    ports:
      - 127.0.0.1:${FRONTEND_PORT}:8080
    environment:
      BACKEND_URL: http://xiaozhi_backend:3010
    depends_on:
      xiaozhi_backend:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "while read _ local _ state _; do case \"$$local $$state\" in *:1F90\\ 0A) exit 0;; esac; done < /proc/net/tcp; exit 1"]
      interval: 10s
      timeout: 5s
      retries: 12
    restart: unless-stopped
    networks: [xiaozhi_net]

  mailpit:
    image: opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi@sha256:e09b9e78336e0e8c2174790ea5ce7587de986884c5640a6ae1deb1ef23c70f5f
    platform: linux/amd64
    environment:
      MP_MAX_MESSAGES: "5000"
      MP_DATABASE: /data/mailpit.db
      MP_SMTP_AUTH_ACCEPT_ANY: "1"
      MP_SMTP_AUTH_ALLOW_INSECURE: "1"
    volumes:
      - ./mailpit:/data
    restart: unless-stopped
    networks: [xiaozhi_net]

  manticoresearch:
    image: opencsg-registry.cn-beijing.cr.aliyuncs.com/opencsghq/xiaozhi@sha256:8b097750d3f62e48f78b6f5cb3fbe20c301aec08a668d7dcae301aced5780993
    platform: linux/amd64
    ulimits:
      nproc: 65535
      nofile:
        soft: 65535
        hard: 65535
    volumes:
      - ./manticore:/var/lib/manticore
    restart: unless-stopped
    networks: [xiaozhi_net]

networks:
  xiaozhi_net:
    driver: bridge
`

type xiaozhiAvailability struct {
	supported bool
	reason    string
}

func (m *Manager) xiaozhiRoot() (string, error) {
	if m.cfg != nil && strings.TrimSpace(m.cfg.StorageDir()) != "" {
		return filepath.Join(m.cfg.StorageDir(), "apps", xiaozhiAppID), nil
	}
	home, err := config.AppHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "apps", xiaozhiAppID), nil
}

func (m *Manager) xiaozhiComposePath() (string, error) {
	root, err := m.xiaozhiRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "compose.yml"), nil
}

func (m *Manager) xiaozhiInstallMarkerPath() (string, error) {
	root, err := m.xiaozhiRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".installed"), nil
}

func xiaozhiDockerAvailability(ctx context.Context) xiaozhiAvailability {
	if !xiaozhiHostCanRunAMD64() {
		return xiaozhiAvailability{reason: "architecture_unsupported"}
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return xiaozhiAvailability{supported: true, reason: "docker_not_found"}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(checkCtx, docker, "compose", "version").Run(); err != nil {
		return xiaozhiAvailability{supported: true, reason: "docker_compose_unavailable"}
	}
	infoCtx, infoCancel := context.WithTimeout(ctx, 3*time.Second)
	defer infoCancel()
	if err := exec.CommandContext(infoCtx, docker, "info").Run(); err != nil {
		return xiaozhiAvailability{supported: true, reason: "docker_daemon_unavailable"}
	}
	return xiaozhiAvailability{supported: true}
}

func xiaozhiHostCanRunAMD64() bool {
	return runtime.GOARCH == "amd64" || (runtime.GOOS == "darwin" && runtime.GOARCH == "arm64")
}

func (m *Manager) cachedXiaozhiAvailability(ctx context.Context) xiaozhiAvailability {
	if time.Now().Before(m.xiaozhiAvailabilityExpires) {
		return m.xiaozhiAvailability
	}
	availability := xiaozhiDockerAvailability(ctx)
	m.xiaozhiAvailability = availability
	m.xiaozhiAvailabilityExpires = time.Now().Add(5 * time.Second)
	return availability
}

func (m *Manager) refreshXiaozhiStateLocked(ctx context.Context, st *appState) {
	availability := m.cachedXiaozhiAvailability(ctx)
	st.info.Supported = availability.supported
	st.info.Disabled = availability.reason != ""
	st.info.DisabledReason = availability.reason
	st.info.ProgressMode = progressModeIndeterminate
	st.info.UpdatedAt = time.Now()
	if availability.reason != "" {
		st.info.Status = "disabled"
		st.info.Phase = availability.reason
		st.info.Installed = m.xiaozhiInstalled()
		st.info.Managed = st.info.Installed
		if root, err := m.xiaozhiRoot(); err == nil && st.info.Installed {
			st.info.InstallPath = root
			st.info.Version = xiaozhiVersion
		}
		return
	}
	st.info.DisabledReason = ""
	st.info.Installed = m.xiaozhiInstalled()
	st.info.Managed = st.info.Installed
	if root, err := m.xiaozhiRoot(); err == nil && st.info.Installed {
		st.info.InstallPath = root
		st.info.Version = xiaozhiVersion
	}
	if st.running {
		return
	}
	if st.info.Installed {
		st.info.Status = "installed"
		st.info.Phase = "installed"
		st.info.Progress = 100
		return
	}
	st.info.Status = "idle"
	st.info.Phase = "ready"
	st.info.Progress = 0
	st.info.InstallPath = ""
	st.info.Version = ""
}

func (m *Manager) xiaozhiInstalled() bool {
	composePath, err := m.xiaozhiComposePath()
	if err != nil {
		return false
	}
	if _, err = os.Stat(composePath); err != nil {
		return false
	}
	markerPath, err := m.xiaozhiInstallMarkerPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(markerPath)
	return err == nil
}

func (m *Manager) startXiaozhiAction(action string) (api.AIAppInfo, error) {
	m.mu.Lock()
	st := m.states[xiaozhiAppID]
	if st == nil {
		m.mu.Unlock()
		return api.AIAppInfo{}, fmt.Errorf("unknown app %q", xiaozhiAppID)
	}
	m.refreshXiaozhiStateLocked(context.Background(), st)
	if st.info.Disabled {
		info := cloneInfo(st.info)
		m.mu.Unlock()
		return info, errors.New("app is disabled")
	}
	if st.running {
		info := cloneInfo(st.info)
		m.mu.Unlock()
		return info, nil
	}
	if action == "uninstall" && !st.info.Installed {
		info := cloneInfo(st.info)
		m.mu.Unlock()
		return info, nil
	}
	st.logBuf.Reset()
	logPath := m.logPath(xiaozhiAppID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		m.mu.Unlock()
		return api.AIAppInfo{}, err
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		m.mu.Unlock()
		return api.AIAppInfo{}, err
	}
	st.running = true
	st.info.Status = actionStatus(action)
	st.info.Phase = "starting"
	st.info.LastError = ""
	st.info.LogPath = logPath
	st.info.UpdatedAt = time.Now()
	info := cloneInfo(st.info)
	m.mu.Unlock()
	go m.runXiaozhiAction(action)
	return info, nil
}

func (m *Manager) runXiaozhiAction(action string) {
	logFile, err := os.OpenFile(m.logPath(xiaozhiAppID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		m.failXiaozhiAction(err)
		return
	}
	defer logFile.Close()
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	var actionErr error
	if action == "uninstall" {
		m.updateProgress(xiaozhiAppID, 10, "stopping")
		actionErr = m.xiaozhiComposeRun(ctx, logFile, "down", "--remove-orphans")
		if actionErr == nil {
			composePath, pathErr := m.xiaozhiComposePath()
			if pathErr == nil {
				actionErr = os.Remove(composePath)
			}
			if markerPath, markerErr := m.xiaozhiInstallMarkerPath(); markerErr == nil {
				if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) && actionErr == nil {
					actionErr = err
				}
			}
		}
	} else {
		m.updateProgress(xiaozhiAppID, 10, "preparing")
		actionErr = m.prepareXiaozhiFiles()
		if actionErr == nil {
			m.updateProgress(xiaozhiAppID, 25, "pulling_images")
			actionErr = m.xiaozhiComposeRun(ctx, logFile, "pull")
		}
		if actionErr == nil {
			markerPath, markerErr := m.xiaozhiInstallMarkerPath()
			if markerErr != nil {
				actionErr = markerErr
			} else {
				actionErr = writeAtomicFile(markerPath, []byte(xiaozhiVersion+"\n"), 0o600)
			}
		}
	}
	if actionErr != nil {
		m.failXiaozhiAction(actionErr)
		return
	}
	m.mu.Lock()
	st := m.states[xiaozhiAppID]
	st.running = false
	st.info.LastError = ""
	st.info.UpdatedAt = time.Now()
	if action == "uninstall" {
		st.info.Status = "idle"
		st.info.Phase = "ready"
		st.info.Progress = 0
		st.info.Installed = false
		st.info.Managed = false
		st.info.InstallPath = ""
		st.info.Version = ""
	} else {
		root, _ := m.xiaozhiRoot()
		st.info.Status = "installed"
		st.info.Phase = "installed"
		st.info.Progress = 100
		st.info.Installed = true
		st.info.Managed = true
		st.info.InstallPath = root
		st.info.Version = xiaozhiVersion
	}
	m.mu.Unlock()
}

func (m *Manager) failXiaozhiAction(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[xiaozhiAppID]
	if st == nil {
		return
	}
	st.running = false
	st.info.Status = "failed"
	st.info.Phase = "failed"
	st.info.LastError = err.Error()
	st.info.UpdatedAt = time.Now()
}

func (m *Manager) prepareXiaozhiFiles() error {
	root, err := m.xiaozhiRoot()
	if err != nil {
		return err
	}
	for _, dir := range []string{"config", "storage", "postgres", "redis", "mailpit", "manticore", "logs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			return fmt.Errorf("creating Xiaozhi %s directory: %w", dir, err)
		}
	}
	composePath := filepath.Join(root, "compose.yml")
	if err := writeAtomicFile(composePath, []byte(xiaozhiComposeYAML), 0o600); err != nil {
		return fmt.Errorf("writing Xiaozhi compose file: %w", err)
	}
	envPath := filepath.Join(root, ".env")
	if _, err := os.Stat(envPath); errors.Is(err, os.ErrNotExist) {
		password, secretErr := randomHex(24)
		if secretErr != nil {
			return secretErr
		}
		env := strings.Join([]string{
			"DB_USERNAME=xiaozhi",
			"DB_PASSWORD=" + password,
			"DB_DATABASE=xiaozhi",
			"XIAOZHI_SERVER_EXTERNAL_URL=http://localhost:8080",
			"XIAOZHI_SERVER_HOST=0.0.0.0",
			"XIAOZHI_SERVER_PORT=3010",
			"XIAOZHI_ENV=production",
			"XIAOZHI_PRIVATE_KEY=",
			"MAILER_HOST=mailpit",
			"MAILER_PORT=1025",
			"MAILER_SENDER=noreply@localhost",
			"MAILER_USER=",
			"MAILER_PASSWORD=",
			"MAILER_IGNORE_TLS=true",
			"MAILER_SERVERNAME=xiaozhi.local",
			"XIAOZHI_ADMIN_EMAIL=csglite@opencsg.com",
			"XIAOZHI_ADMIN_PASSWORD=csglite",
			fmt.Sprintf("FRONTEND_PORT=%d", xiaozhiPort),
			"",
		}, "\n")
		if err := writeAtomicFile(envPath, []byte(env), 0o600); err != nil {
			return fmt.Errorf("writing Xiaozhi environment: %w", err)
		}
	}
	if err := ensureXiaozhiEnvDefaults(envPath, []string{
		"XIAOZHI_ADMIN_EMAIL=csglite@opencsg.com",
		"XIAOZHI_ADMIN_PASSWORD=csglite",
	}); err != nil {
		return err
	}
	if err := ensureXiaozhiPrivateKey(filepath.Join(root, "config", "private.key")); err != nil {
		return err
	}
	return nil
}

func ensureXiaozhiEnvDefaults(path string, defaults []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading Xiaozhi environment: %w", err)
	}
	original := string(data)
	content := original
	content = xiaozhiReplaceEnvValue(
		content,
		"XIAOZHI_ADMIN_EMAIL",
		"admin@csghub-lite.local",
		"csglite@opencsg.com",
	)
	content = xiaozhiReplaceEnvValue(content, "XIAOZHI_ADMIN_PASSWORD", "csghub-lite", "csglite")
	content = xiaozhiRemoveEnvEntry(content, "XIAOZHI_ADMIN_USERNAME", "csglite")
	var additions []string
	for _, entry := range defaults {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			return fmt.Errorf("invalid Xiaozhi environment default %q", entry)
		}
		if !xiaozhiEnvHasKey(content, key) {
			additions = append(additions, entry)
		}
	}
	if len(additions) == 0 && content == original {
		return nil
	}
	if len(additions) > 0 {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += strings.Join(additions, "\n") + "\n"
	}
	if err := writeAtomicFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("updating Xiaozhi environment: %w", err)
	}
	return nil
}

func xiaozhiEnvHasKey(content, key string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, _, ok := strings.Cut(line, "="); ok && strings.TrimSpace(name) == key {
			return true
		}
	}
	return false
}

func xiaozhiReplaceEnvValue(content, key, oldValue, newValue string) string {
	lines := strings.Split(content, "\n")
	oldEntry := key + "=" + oldValue
	for index, line := range lines {
		if strings.TrimSpace(line) == oldEntry {
			lines[index] = key + "=" + newValue
		}
	}
	return strings.Join(lines, "\n")
}

func xiaozhiRemoveEnvEntry(content, key, value string) string {
	lines := strings.Split(content, "\n")
	entry := key + "=" + value
	filtered := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) != entry {
			filtered = append(filtered, line)
		}
	}
	return strings.Join(filtered, "\n")
}

func ensureXiaozhiPrivateKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking Xiaozhi private key: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating Xiaozhi private key: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("encoding Xiaozhi private key: %w", err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := writeAtomicFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing Xiaozhi private key: %w", err)
	}
	return nil
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func randomHex(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generating random secret: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func (m *Manager) xiaozhiComposeCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("docker not found: %w", err)
	}
	root, err := m.xiaozhiRoot()
	if err != nil {
		return nil, err
	}
	composePath := filepath.Join(root, "compose.yml")
	baseArgs := []string{"compose", "--project-name", xiaozhiProjectName, "--file", composePath}
	cmd := exec.CommandContext(ctx, docker, append(baseArgs, args...)...)
	cmd.Dir = root
	if m.cfg != nil {
		tmp := filepath.Join(m.cfg.TempDir(), "apps", xiaozhiAppID)
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return nil, err
		}
		cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
	}
	return cmd, nil
}

func (m *Manager) xiaozhiComposeRun(ctx context.Context, logFile *os.File, args ...string) error {
	cmd, err := m.xiaozhiComposeCommand(ctx, args...)
	if err != nil {
		return err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	m.appendLog(xiaozhiAppID, logFile, "INFO: docker compose "+strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (m *Manager) StartXiaozhi(ctx context.Context) error {
	if !m.xiaozhiInstalled() {
		return errors.New("xiaozhi is not installed yet")
	}
	if running, _ := m.XiaozhiRunning(ctx); !running {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", xiaozhiPort))
		if err != nil {
			return fmt.Errorf("Xiaozhi port %d is already in use", xiaozhiPort)
		}
		_ = listener.Close()
	}
	logFile, err := os.OpenFile(m.logPath(xiaozhiAppID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	if err := m.xiaozhiComposeRun(ctx, logFile, "up", "-d"); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", xiaozhiPort), time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("Xiaozhi frontend did not become ready in time")
}

func (m *Manager) StopXiaozhi(ctx context.Context) error {
	logFile, err := os.OpenFile(m.logPath(xiaozhiAppID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	return m.xiaozhiComposeRun(ctx, logFile, "stop")
}

func (m *Manager) XiaozhiRunning(ctx context.Context) (bool, error) {
	if !m.xiaozhiInstalled() {
		return false, nil
	}
	cmd, err := m.xiaozhiComposeCommand(ctx, "ps", "--status", "running", "--services")
	if err != nil {
		return false, err
	}
	out, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	services := string(out)
	return strings.Contains(services, "xiaozhi_backend") && strings.Contains(services, "xiaozhi_frontend"), nil
}

func XiaozhiURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", xiaozhiPort)
}
