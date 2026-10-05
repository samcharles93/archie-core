#!/usr/bin/env bash
# ==============================================================================
# install.sh - Installer for Archie Core (native archied + managed agent image)
#
# Follows the XDG Base Directory Specification:
#   Config: ${XDG_CONFIG_HOME:-~/.config}/archie
#     - config.toml
#     - env (environment variables & API secrets)
#     - persona/
#     - skills/
#   Data:   ${XDG_DATA_HOME:-~/.local/share}/archie   (state_dir)
#     - nats/
#     - logs/tasks/
#     - work/
#     - memories/
#     - tasks/
#     - plugins/
#   Bin:    ${XDG_BIN_HOME:-~/.local/bin}
#     - archied
# ==============================================================================

set -euo pipefail

# Determine script location / repo root if executed from within archie-core
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_URL="https://github.com/samcharles93/archie-core.git"

# XDG Base Directory resolution
XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-${HOME}/.config}"
XDG_DATA_HOME="${XDG_DATA_HOME:-${HOME}/.local/share}"
XDG_BIN_HOME="${XDG_BIN_HOME:-${HOME}/.local/bin}"

ARCHIE_CONFIG_DIR="${XDG_CONFIG_HOME}/archie"
ARCHIE_DATA_DIR="${XDG_DATA_HOME}/archie"
ARCHIE_BIN_DIR="${XDG_BIN_HOME}"
ENV_FILE="${ARCHIE_CONFIG_DIR}/env"

# Parse optional arguments
INSTALL_SYSTEMD=true
ENABLE_LINGER=true
AUTO_START=true
INTERACTIVE=true

[ ! -t 0 ] && INTERACTIVE=false

usage() {
  echo "Usage: ./install.sh [options]"
  echo ""
  echo "Options:"
  echo "  --no-systemd       Skip systemd user unit installation"
  echo "  --no-linger        Skip loginctl enable-linger execution"
  echo "  --no-start         Install service but do not auto-start archied"
  echo "  --non-interactive  Run without interactive setup prompts"
  echo "  --help, -h         Show this help message"
  echo ""
  echo "XDG Target Directories:"
  echo "  Config: ${ARCHIE_CONFIG_DIR}"
  echo "  Data:   ${ARCHIE_DATA_DIR}"
  echo "  Bin:    ${ARCHIE_BIN_DIR}"
}

for arg in "$@"; do
  case "$arg" in
    --no-systemd)
      INSTALL_SYSTEMD=false
      AUTO_START=false
      shift
      ;;
    --no-linger)
      ENABLE_LINGER=false
      shift
      ;;
    --no-start)
      AUTO_START=false
      shift
      ;;
    --non-interactive|--batch)
      INTERACTIVE=false
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Error: unknown option '${arg}'" >&2
      echo "" >&2
      usage >&2
      exit 1
      ;;
  esac
done

echo "============================================================"
echo "  Archie Core Installer (XDG Standard)"
echo "============================================================"
echo "Config directory : ${ARCHIE_CONFIG_DIR}"
echo "Data directory   : ${ARCHIE_DATA_DIR}"
echo "Bin directory    : ${ARCHIE_BIN_DIR}"
echo "============================================================"
echo ""

# 1. Prerequisite checks
echo "==> Checking prerequisites..."

# Linux only, deliberately. The installer depends on systemd user units and
# loginctl linger, and uses GNU sed/bash 4 semantics throughout. Failing here
# with a clear message beats failing three steps later inside a heredoc.
if [ "$(uname -s)" != "Linux" ]; then
  echo "Error: archie-core's installer supports Linux only (needs systemd and loginctl)." >&2
  echo "       Detected: $(uname -s)" >&2
  exit 1
fi

for cmd in git go jq; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "Error: '$cmd' is required but not installed." >&2
    exit 1
  fi
done

GO_VERSION="$(go version | awk '{print $3}')"
echo "  [OK] git: $(git --version)"
echo "  [OK] go: ${GO_VERSION}"

if command -v docker &>/dev/null; then
  echo "  [OK] docker: $(docker --version)"
else
  echo "  [WARN] Docker is required for autonomous workflows; chat and the dashboard can still run without it."
fi

# 2. Create directory structure according to XDG standards
echo "==> Creating XDG directory structure..."
mkdir -p "${ARCHIE_CONFIG_DIR}"/{persona,skills}
mkdir -p "${ARCHIE_DATA_DIR}"/{work,memories,tasks,plugins}
mkdir -p "${ARCHIE_BIN_DIR}"

# 3. Determine source location
SRC_DIR=""
if [ -f "${SCRIPT_DIR}/go.mod" ] && grep -q "github.com/samcharles93/archie-core" "${SCRIPT_DIR}/go.mod"; then
  SRC_DIR="${SCRIPT_DIR}"
  echo "==> Installing from local repository: ${SRC_DIR}"
else
  SRC_DIR="${ARCHIE_DATA_DIR}/src/archie-core"
  if [ -d "${SRC_DIR}/.git" ]; then
    # The shipped tree is Archie's own working copy, so it may legitimately
    # carry local commits or edits it made while investigating a fault. A bare
    # `pull --rebase` fails on those and, under `set -e`, aborts the install.
    # Local state wins: report it and build what is on disk.
    if [ -n "$(git -C "${SRC_DIR}" status --porcelain)" ]; then
      echo "==> Local changes in ${SRC_DIR}; skipping update and building as-is."
    elif ! git -C "${SRC_DIR}" pull --ff-only; then
      echo "==> Could not fast-forward ${SRC_DIR}; building the existing checkout."
    fi
  else
    echo "==> Cloning archie-core repository to ${SRC_DIR}..."
    mkdir -p "$(dirname "${SRC_DIR}")"
    git clone "${REPO_URL}" "${SRC_DIR}"
  fi
fi

TOPOLOGY_UNITS="$(jq -er '.required_topology.units | join(" ")' "$SRC_DIR/release.json")"

# 4. Build and install the native daemon. archie-agent is deployed only as
# the managed task image; installing a host binary would imply an unsupported
# host execution path.
echo "==> Building native archie binaries..."
(
  cd "${SRC_DIR}"
  # installtype.buildType must be stamped here: an unstamped archied
  # refuses to self-update (internal/releaseupdate.ErrUnknownInstallType)
  # rather than guess whether /update's configured install command is
  # even the right kind of update for a script-built native binary.
  # A from-source build is not a release, and CI is the only thing that stamps
  # one: the version a release carries is written into the artifact CI
  # publishes -- the zip's binaries and the container image -- and is never
  # derived from whatever tags a local checkout happens to have. Deriving it
  # here made a source-built host claim a release it was not running, and made
  # the stamp a function of the commit graph, which is the one input a
  # content-checksum build cache cannot see. A source build
  # says `dev`; its traceability is the checkout it came from.
  GATEWAY_VERSION="dev"
  RUNTIME_VERSION="dev"
  LDFLAGS="-X github.com/samcharles93/archie-core/internal/buildinfo.Version=${GATEWAY_VERSION}"
  LDFLAGS="${LDFLAGS} -X github.com/samcharles93/archie-core/internal/buildinfo.Runtime=${RUNTIME_VERSION}"
  LDFLAGS="${LDFLAGS} -X github.com/samcharles93/archie-core/internal/installtype.buildType=binary"
  # Every host binary carries the release it was built from, so the updater can
  # ask each installed binary what it is.
  LDFLAGS="${LDFLAGS} -X github.com/samcharles93/archie-core/internal/buildinfo.Version=${GATEWAY_VERSION}"
  LDFLAGS="${LDFLAGS} -X github.com/samcharles93/archie-core/internal/buildinfo.Runtime=${RUNTIME_VERSION}"
  # archied does not run alone: the State Store owns the task data, the Gateway
  # serves the chat contract, the dashboard is its own process, and the
  # Messaging Service owns the chat channels. Building only archied leaves it
  # unable to boot, and omitting archie-messaging leaves the Telegram, email and
  # webhook channels dead with no error anywhere. This list,
  # the zip's two lists and the two in scripts/archie-update-install must agree;
  # TestDistZipShipsEveryHostCommand fails when they do not.
  for cmd in $TOPOLOGY_UNITS; do
    go build -ldflags "${LDFLAGS}" -o "${ARCHIE_BIN_DIR}/${cmd}" "./cmd/${cmd}"
  done
  install -m755 "${SRC_DIR}"/scripts/archie-update-* "${ARCHIE_BIN_DIR}/"
)
echo "  Installed archied, archie-gateway, archie-state-store, archie-ui, archie-messaging and updater to ${ARCHIE_BIN_DIR}/"

# 5. Configuration. archied setup asks every question and writes config.toml
# plus any secrets to the env file beside it; this script asks nothing itself.
if [ ! -f "${ARCHIE_CONFIG_DIR}/config.toml" ]; then
  echo "==> Generating config.toml (archied setup)..."
  if [ "${INTERACTIVE}" = true ]; then
    setup_args=(-config "${ARCHIE_CONFIG_DIR}/config.toml")
  else
    # Unattended: no forge and keyless Ollama, so the services boot without a secret.
    setup_args=(--defaults -config "${ARCHIE_CONFIG_DIR}/config.toml" -forge-type none)
  fi
  if ! "${ARCHIE_BIN_DIR}/archied" setup "${setup_args[@]}"; then
    echo "ERROR: archied setup could not generate ${ARCHIE_CONFIG_DIR}/config.toml" >&2
    exit 1
  fi
else
  echo "  [SKIP] ${ARCHIE_CONFIG_DIR}/config.toml already exists. Re-run 'archied setup' to change it."
fi

# Every service fails closed without PostgreSQL. The default database_url is
# the Compose postgres service, so start it when the config still uses it.
if ! grep -qF '@127.0.0.1:5432/archie' "${ARCHIE_CONFIG_DIR}/config.toml"; then
  echo "  [OK] Using the PostgreSQL server named by database_url."
elif command -v docker &>/dev/null && [ -f "${SRC_DIR}/docker-compose.yml" ]; then
  echo "==> Starting PostgreSQL 18 (docker compose up -d postgres)..."
  docker compose --env-file "${ENV_FILE}" -f "${SRC_DIR}/docker-compose.yml" up -d postgres ||
    echo "  [WARN] Could not start PostgreSQL. Start it before the services, or point database_url at your own PostgreSQL 18."
else
  echo "  [WARN] Docker not found: point database_url in ${ARCHIE_CONFIG_DIR}/config.toml at a PostgreSQL 18 server before starting the services."
fi

# 6. Seed skills, personas, memories, and onboarding tasks
echo "==> Seeding skills and templates..."

# Seed example personas without overwriting existing files
if [ -d "${SRC_DIR}/examples/persona" ]; then
  cp -rn "${SRC_DIR}/examples/persona/"* "${ARCHIE_CONFIG_DIR}/persona/" 2>/dev/null || true
fi

# Seed initial tasks without overwriting existing files
if [ -d "${SRC_DIR}/examples/tasks" ]; then
  cp -rn "${SRC_DIR}/examples/tasks/"* "${ARCHIE_DATA_DIR}/tasks/" 2>/dev/null || true
fi

# 7. Systemd user service setup & linger configuration
SERVICE_INSTALLED=false
if [ "${INSTALL_SYSTEMD}" = true ] && command -v systemctl &>/dev/null && [ -d "${XDG_CONFIG_HOME}" ]; then
  SYSTEMD_USER_DIR="${XDG_CONFIG_HOME}/systemd/user"
  echo "==> Configuring systemd user services..."
  mkdir -p "${SYSTEMD_USER_DIR}"

  # Every service this installer builds gets a unit, with the contents the
  # runbook used to tell an operator to paste by hand. The runbook is a
  # description now, not the mechanism: a fresh install must not need four
  # hand-written units before its own updater will run, and
  # the contents must not live in two places to drift apart.
  cat <<EOF > "${SYSTEMD_USER_DIR}/archied.service"
[Unit]
Description=Archie Core Orchestrator Daemon
After=network.target
# Give up after 5 failures in 5 minutes rather than restarting forever. A
# genuinely broken install otherwise loops every RestartSec indefinitely,
# burying the real error in the journal instead of surfacing a failed unit.
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
ExecStart=${ARCHIE_BIN_DIR}/archied -config ${ARCHIE_CONFIG_DIR}/config.toml
EnvironmentFile=-${ENV_FILE}
# archied reloads its configuration on SIGHUP. Without ExecReload,
# 'systemctl --user reload archied' fails and the operator has to know
# to run 'systemctl --user kill -s HUP' instead.
ExecReload=/bin/kill -HUP \$MAINPID
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
EOF

  # The State Store owns the task data and is the process everything else dials, so
  # its unit is the first dependency the others name.
  cat <<EOF > "${SYSTEMD_USER_DIR}/archie-state-store.service"
[Unit]
Description=Archie State Store Service
After=network.target

[Service]
Type=simple
ExecStart=${ARCHIE_BIN_DIR}/archie-state-store -config ${ARCHIE_CONFIG_DIR}/config.toml -listen 127.0.0.1:9090 -ready-addr 127.0.0.1:9091
EnvironmentFile=-${ENV_FILE}
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
EOF

  cat <<EOF > "${SYSTEMD_USER_DIR}/archie-gateway.service"
[Unit]
Description=Archie Gateway Service
After=network.target

[Service]
Type=simple
ExecStart=${ARCHIE_BIN_DIR}/archie-gateway -config ${ARCHIE_CONFIG_DIR}/config.toml -listen 127.0.0.1:8585
EnvironmentFile=-${ENV_FILE}
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
EOF

  cat <<EOF > "${SYSTEMD_USER_DIR}/archie-ui.service"
[Unit]
Description=Archie UI Service
After=network.target archie-state-store.service
Wants=archie-state-store.service

[Service]
Type=simple
ExecStart=${ARCHIE_BIN_DIR}/archie-ui -config ${ARCHIE_CONFIG_DIR}/config.toml
EnvironmentFile=-${ENV_FILE}
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
EOF

  # The Telegram, email and webhook channels run here, not in archied: without
  # this unit a host looks healthy while its channels are dead.
  cat <<EOF > "${SYSTEMD_USER_DIR}/archie-messaging.service"
[Unit]
Description=Archie Messaging Service
After=network.target archie-gateway.service archie-state-store.service
Wants=archie-gateway.service archie-state-store.service

[Service]
Type=simple
ExecStart=${ARCHIE_BIN_DIR}/archie-messaging -config ${ARCHIE_CONFIG_DIR}/config.toml
EnvironmentFile=-${ENV_FILE}
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
EOF

  systemctl --user daemon-reload || true
  SERVICE_INSTALLED=true
  for unit in $TOPOLOGY_UNITS; do
    test -f "${SYSTEMD_USER_DIR}/${unit}.service" || { echo "missing required unit: $unit" >&2; exit 1; }
    echo "  Installed ${SYSTEMD_USER_DIR}/${unit}.service"
  done

  # Enable linger so systemd --user runs continuously without an active login
  # session. $(id -un) is used rather than $USER, which is unset in some
  # container and cron contexts and would abort under `set -u`. Only a
  # positive "Linger=yes" counts as already enabled -- a failed or empty
  # `loginctl show-user` (no logind session, a container) falls through to
  # enable-linger rather than being reported as already OK, since nothing
  # was actually observed. enable-linger is idempotent, so a redundant call
  # here is harmless.
  if [ "${ENABLE_LINGER}" = true ] && command -v loginctl &>/dev/null; then
    LINGER_USER="$(id -un)"
    if loginctl show-user "${LINGER_USER}" 2>/dev/null | grep -q "Linger=yes"; then
      echo "  [OK] User linger is already enabled."
    else
      echo "==> Enabling systemd user linger (loginctl enable-linger ${LINGER_USER})..."
      loginctl enable-linger "${LINGER_USER}" || echo "  Notice: Could not enable linger automatically. Run 'loginctl enable-linger ${LINGER_USER}' manually if needed."
    fi
  fi

  # Auto-enable and start the services, the store first so a fresh boot does
  # not race a dial it depends on.
  if [ "${AUTO_START}" = true ]; then
    echo "==> Enabling and starting Archie services..."
    systemctl --user enable --now $TOPOLOGY_UNITS ||
      echo "  Notice: Could not start every service automatically. Check: systemctl --user status archied archie-state-store archie-gateway archie-ui archie-messaging"
  fi
fi

# 8. Post-installation summary & instructions
echo ""
echo "============================================================"
echo "  ✓ Archie Core Installation Complete!"
echo "============================================================"
echo ""
echo "Installation Details:"
echo "  - Binaries   : ${ARCHIE_BIN_DIR}/{archied,archie-state-store,archie-gateway,archie-ui,archie-messaging}"
echo "  - Agent image: ghcr.io/samcharles93/archie-agent:latest"
echo "  - Config     : ${ARCHIE_CONFIG_DIR}/config.toml"
echo "  - Secrets    : ${ENV_FILE}"
echo "  - Data       : ${ARCHIE_DATA_DIR}/"
echo ""

# Check PATH
if [[ ":$PATH:" != *":${ARCHIE_BIN_DIR}:"* ]]; then
  echo "NOTE: ${ARCHIE_BIN_DIR} is not in your current PATH."
  echo "Add it to your shell configuration file (~/.bashrc or ~/.zshrc):"
  echo "  export PATH=\"${ARCHIE_BIN_DIR}:\$PATH\""
  echo ""
fi

if [ "${SERVICE_INSTALLED}" = true ] && [ "${AUTO_START}" = true ]; then
  echo "Service Management:"
  echo "  - Units installed   : archied archie-state-store archie-gateway archie-ui archie-messaging"
  echo "  - View live logs    : journalctl --user -u archied -f"
  echo "  - Service status    : systemctl --user status archied archie-state-store archie-gateway archie-ui archie-messaging"
  echo "  - Restart daemon    : systemctl --user restart archied"
else
  echo "Manual Startup: start the five services as in ${SRC_DIR}/deployments/README.md"
fi
echo ""
echo "Dashboard: http://127.0.0.1:8484   Change the config: archied setup"
echo ""
