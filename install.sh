#!/usr/bin/env bash
# install.sh - installs Archie Core for the current user.
#
#   curl -fsSL https://raw.githubusercontent.com/samcharles93/archie-core/main/install.sh | bash
#
# Downloads the latest release, verifies it against SHA256SUMS, installs the
# five services to ${XDG_BIN_HOME:-~/.local/bin}, runs `archied setup`, starts
# the bundled PostgreSQL and installs systemd user units.
# Config: ${XDG_CONFIG_HOME:-~/.config}/archie   Data: ${XDG_DATA_HOME:-~/.local/share}/archie

set -euo pipefail

REPO="samcharles93/archie-core"

XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-${HOME}/.config}"
XDG_DATA_HOME="${XDG_DATA_HOME:-${HOME}/.local/share}"
XDG_BIN_HOME="${XDG_BIN_HOME:-${HOME}/.local/bin}"

ARCHIE_CONFIG_DIR="${XDG_CONFIG_HOME}/archie"
ARCHIE_DATA_DIR="${XDG_DATA_HOME}/archie"
ARCHIE_BIN_DIR="${XDG_BIN_HOME}"
ENV_FILE="${ARCHIE_CONFIG_DIR}/env"

INSTALL_SYSTEMD=true
ENABLE_LINGER=true
AUTO_START=true
FROM_SOURCE=false
# Piped into bash, stdin is the script; setup reads its answers from the
# terminal instead, so a pipe can still be interactive.
INTERACTIVE=false
if (: </dev/tty) 2>/dev/null; then INTERACTIVE=true; fi

usage() {
  cat <<EOF
Usage: install.sh [options]
   or: curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | bash -s -- [options]

Options:
  --version X.Y.Z    Install this release instead of the latest
  --from-source      Build from a checkout (this one, or a fresh clone) instead
                     of a release; needs Go, git and jq
  --no-systemd       Skip systemd user units
  --no-linger        Skip loginctl enable-linger
  --no-start         Install the units but do not start them
  --non-interactive  Write an unattended config (no forge, Ollama)
  --help, -h         Show this help
EOF
}

VERSION=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?--version needs a value}"; shift ;;
    --from-source) FROM_SOURCE=true ;;
    --no-systemd) INSTALL_SYSTEMD=false; AUTO_START=false ;;
    --no-linger) ENABLE_LINGER=false ;;
    --no-start) AUTO_START=false ;;
    --non-interactive|--batch) INTERACTIVE=false ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Error: unknown option '$1'" >&2; usage >&2; exit 1 ;;
  esac
  shift
done

need() {
  for cmd in "$@"; do
    command -v "$cmd" &>/dev/null || { echo "Error: '$cmd' is required but not installed." >&2; exit 1; }
  done
}

# 1. Prerequisites
echo "==> Checking prerequisites..."
# The installer depends on systemd user units and GNU tools.
if [ "$(uname -s)" != "Linux" ]; then
  echo "Error: the installer supports Linux only. Detected: $(uname -s)" >&2
  exit 1
fi
if [ "${FROM_SOURCE}" = true ]; then
  need git go jq
else
  [ "$(uname -m)" = x86_64 ] || { echo "Error: releases are built for x86_64 only; use --from-source." >&2; exit 1; }
  need curl unzip sha256sum
fi
if command -v docker &>/dev/null; then
  echo "  [OK] $(docker --version)"
else
  echo "  [WARN] Docker not found. Agents run in containers and the bundled PostgreSQL needs it."
fi

mkdir -p "${ARCHIE_CONFIG_DIR}" "${ARCHIE_DATA_DIR}" "${ARCHIE_BIN_DIR}"

# 2. Binaries. STAGE ends up holding the binaries, release.json and the
# updater scripts, whichever way they were obtained.
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

if [ "${FROM_SOURCE}" = true ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
  if [ -f "${SCRIPT_DIR}/go.mod" ] && grep -q "github.com/${REPO}" "${SCRIPT_DIR}/go.mod"; then
    SRC_DIR="${SCRIPT_DIR}"
  else
    SRC_DIR="${TMP}/src"
    git clone --quiet --depth=1 "https://github.com/${REPO}.git" "${SRC_DIR}"
  fi
  echo "==> Building from ${SRC_DIR}..."
  STAGE="${TMP}/stage"
  mkdir -p "${STAGE}"
  # A source build is not a release: it says `dev`, and buildType=binary lets
  # it self-update.
  LDFLAGS="-X github.com/${REPO}/internal/buildinfo.Version=dev -X github.com/${REPO}/internal/buildinfo.Runtime=dev -X github.com/${REPO}/internal/installtype.buildType=binary"
  for cmd in $(jq -er '.required_topology.units | join(" ")' "${SRC_DIR}/release.json"); do
    (cd "${SRC_DIR}" && go build -ldflags "${LDFLAGS}" -o "${STAGE}/${cmd}" "./cmd/${cmd}")
  done
  cp "${SRC_DIR}/release.json" "${SRC_DIR}"/scripts/archie-update-* "${STAGE}/"
else
  if [ -z "${VERSION}" ]; then
    tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')"
    [ -n "${tag}" ] || { echo "Error: could not find the latest release." >&2; exit 1; }
  else
    tag="v${VERSION}"
  fi
  VERSION="${tag##*v}"
  name="archie-core-archied-v${VERSION}-linux-amd64"
  echo "==> Downloading Archie ${VERSION}..."
  base="https://github.com/${REPO}/releases/download/${tag}"
  curl -fsSL "${base}/${name}.zip" -o "${TMP}/${name}.zip"
  curl -fsSL "${base}/SHA256SUMS" -o "${TMP}/SHA256SUMS"
  expected="$(awk -v n="${name}.zip" '$2 == n {print $1}' "${TMP}/SHA256SUMS")"
  [[ "${expected}" =~ ^[a-f0-9]{64}$ ]] || { echo "Error: ${name}.zip is not in SHA256SUMS." >&2; exit 1; }
  printf '%s  %s\n' "${expected}" "${TMP}/${name}.zip" | sha256sum --check --status ||
    { echo "Error: checksum mismatch for ${name}.zip." >&2; exit 1; }
  unzip -q "${TMP}/${name}.zip" -d "${TMP}"
  STAGE="${TMP}/${name}"
fi

TOPOLOGY_UNITS="$(tr -d '\n' <"${STAGE}/release.json" | sed -n 's/.*"units": *\[\([^]]*\)\].*/\1/p' | tr -d '" ' | tr , ' ')"
for cmd in ${TOPOLOGY_UNITS}; do
  install -m755 "${STAGE}/${cmd}" "${ARCHIE_BIN_DIR}/${cmd}"
done
install -m755 "${STAGE}"/archie-update-* "${ARCHIE_BIN_DIR}/"
echo "  Installed ${TOPOLOGY_UNITS} and the updater to ${ARCHIE_BIN_DIR}/"

# 3. Configuration. archied setup asks every question and writes config.toml
# plus any secrets to the env file beside it; this script asks nothing itself.
if [ ! -f "${ARCHIE_CONFIG_DIR}/config.toml" ]; then
  echo "==> Generating config.toml (archied setup)..."
  if [ "${INTERACTIVE}" = true ]; then
    setup_args=(-config "${ARCHIE_CONFIG_DIR}/config.toml")
  else
    # Unattended: no forge and keyless Ollama, so the services boot without a secret.
    setup_args=(--defaults -config "${ARCHIE_CONFIG_DIR}/config.toml" -forge-type none)
  fi
  setup_in=/dev/null
  [ "${INTERACTIVE}" = true ] && setup_in=/dev/tty
  if ! "${ARCHIE_BIN_DIR}/archied" setup "${setup_args[@]}" <"${setup_in}"; then
    echo "ERROR: archied setup could not generate ${ARCHIE_CONFIG_DIR}/config.toml" >&2
    exit 1
  fi
else
  echo "  [SKIP] ${ARCHIE_CONFIG_DIR}/config.toml already exists. Re-run 'archied setup' to change it."
fi

# 4. PostgreSQL. Every service fails closed without it. The bundled database
# has no network at all: the services reach it through its Unix socket,
# mounted at PG_SOCKET_DIR, so it publishes no port and cannot collide with
# another PostgreSQL. Its password is the PGPASSWORD setup generated.
PG_SOCKET_DIR="${ARCHIE_DATA_DIR}/postgres"
PG_COMPOSE="${ARCHIE_DATA_DIR}/postgres.yml"
if ! grep -qF "host=${PG_SOCKET_DIR}" "${ARCHIE_CONFIG_DIR}/config.toml"; then
  echo "  [OK] Using the PostgreSQL server named by database_url."
elif ! command -v docker &>/dev/null; then
  echo "  [WARN] Docker not found: the bundled PostgreSQL needs it. Install Docker, or run 'archied setup' with your own PostgreSQL 18 URL." >&2
  AUTO_START=false
else
  echo "==> Starting the bundled PostgreSQL 18..."
  mkdir -p "${PG_SOCKET_DIR}"
  cat >"${PG_COMPOSE}" <<'EOF'
services:
  postgres:
    image: postgres:18
    network_mode: none
    environment:
      POSTGRES_USER: archie
      POSTGRES_PASSWORD: ${PGPASSWORD:-}
      POSTGRES_DB: archie
      POSTGRES_INITDB_ARGS: --auth-local=scram-sha-256
      PGDATA: /var/lib/postgresql/data/pgdata
    volumes:
      - pg_data:/var/lib/postgresql/data
      # :z relabels the directory for SELinux hosts.
      - ./postgres:/var/run/postgresql:z
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U archie -d archie"]
      interval: 5s
      timeout: 5s
      retries: 10
volumes:
  pg_data:
EOF
  if ! docker compose -p archie --env-file "${ENV_FILE}" -f "${PG_COMPOSE}" up -d --wait; then
    echo "  [WARN] The bundled PostgreSQL did not start, so the services are installed but not started." >&2
    echo "         Check: docker compose -p archie -f ${PG_COMPOSE} logs" >&2
    AUTO_START=false
  fi
fi

# Extensions run in an unprivileged user namespace. Ubuntu 23.10+ forbids
# those unless an AppArmor profile allows them, and only root can add one, so
# print the profile that allows it for these binaries alone.
if [ "$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null)" = 1 ]; then
  echo "  [WARN] This host restricts user namespaces, so extensions will not start until you run:" >&2
  cat >&2 <<EOF
    sudo tee /etc/apparmor.d/archie >/dev/null <<'PROFILE'
    abi <abi/4.0>,
    include <tunables/global>
    profile archie ${ARCHIE_BIN_DIR}/archie{d,-*} flags=(unconfined) {
      userns,
    }
    PROFILE
    sudo apparmor_parser -r /etc/apparmor.d/archie
EOF
fi

# 5. Systemd user service setup & linger configuration
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

# 6. Post-installation summary & instructions
echo ""
echo "============================================================"
echo "  ✓ Archie Core Installation Complete!"
echo "============================================================"
echo ""
echo "Installation Details:"
echo "  - Binaries   : ${ARCHIE_BIN_DIR}/{archied,archie-state-store,archie-gateway,archie-ui,archie-messaging}"
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
  echo "Manual Startup: start the five services as in https://github.com/${REPO}/blob/main/deployments/README.md"
fi
echo ""
echo "Dashboard: http://127.0.0.1:8484   Change the config: archied setup"
echo ""
