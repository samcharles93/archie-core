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

for cmd in git go; do
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
  # content-checksum build cache cannot see (archie-core-pdq2). A source build
  # says `dev`; its traceability is the checkout it came from.
  GATEWAY_VERSION="dev"
  RUNTIME_VERSION="dev"
  LDFLAGS="-X github.com/samcharles93/archie-core/internal/app/archied.gatewayVersion=${GATEWAY_VERSION}"
  LDFLAGS="${LDFLAGS} -X github.com/samcharles93/archie-core/internal/app/archied.runtimeVersion=${RUNTIME_VERSION}"
  LDFLAGS="${LDFLAGS} -X github.com/samcharles93/archie-core/internal/installtype.buildType=binary"
  # Every host binary carries the release it was built from, so the updater can
  # ask each installed binary what it is (archie-core-k94o).
  LDFLAGS="${LDFLAGS} -X github.com/samcharles93/archie-core/internal/buildinfo.Version=${GATEWAY_VERSION}"
  LDFLAGS="${LDFLAGS} -X github.com/samcharles93/archie-core/internal/buildinfo.Runtime=${RUNTIME_VERSION}"
  # archied does not run alone: the State Store owns the task data, the Gateway
  # serves the chat contract, the dashboard is its own process, and the
  # Messaging Service owns the chat channels. Building only archied leaves it
  # unable to boot, and omitting archie-messaging leaves the Telegram, email and
  # webhook channels dead with no error anywhere (archie-core-1c01). This list,
  # the zip's two lists and the two in scripts/archie-update-install must agree;
  # TestDistZipShipsEveryHostCommand fails when they do not.
  for cmd in archied archie-gateway archie-state-store archie-ui archie-messaging archie-playbooks; do
    go build -ldflags "${LDFLAGS}" -o "${ARCHIE_BIN_DIR}/${cmd}" "./cmd/${cmd}"
  done
  install -m755 "${SRC_DIR}/scripts/archie-update-install" "${ARCHIE_BIN_DIR}/archie-update-install"
)
echo "  Installed archied, archie-gateway, archie-state-store, archie-ui, archie-messaging, archie-playbooks and updater to ${ARCHIE_BIN_DIR}/"

# 5. Interactive Configuration: Forge & LLM Provider Setup
if [ ! -f "${ENV_FILE}" ]; then
  touch "${ENV_FILE}"
  chmod 600 "${ENV_FILE}"
fi

# read_secret prompts without echoing. A token typed at a visible prompt stays
# in terminal scrollback and in any screen recording, so it is never echoed
# even though that costs the usual typo feedback.
read_secret() {
  local prompt="$1" varname="$2" value=""
  read -rsp "${prompt}" value || value=""
  echo >&2
  printf -v "${varname}" '%s' "${value}"
}

# set_env_key writes KEY=value to the env file, replacing any existing entry.
# Appending meant a second install run left the revoked token above the new one
# and grew the file without bound.
set_env_key() {
  local key="$1" value="$2" tmp
  tmp="$(mktemp "${ARCHIE_CONFIG_DIR}/.env.XXXXXX")"
  chmod 600 "${tmp}"
  if [ -f "${ENV_FILE}" ]; then
    grep -v "^${key}=" "${ENV_FILE}" > "${tmp}" || true
  fi
  # Single quotes with embedded-quote escaping: systemd's EnvironmentFile takes
  # the value literally, so a token containing " or \ must not be re-quoted.
  printf "%s='%s'\n" "${key}" "${value//\'/\'\\\'\'}" >> "${tmp}"
  mv "${tmp}" "${ENV_FILE}"
  chmod 600 "${ENV_FILE}"
}

# Detect existing environment keys
FOUND_ENV_KEYS=()
[ -n "${OPENAI_API_KEY:-}" ] && FOUND_ENV_KEYS+=("OpenAI")
[ -n "${ANTHROPIC_API_KEY:-}" ] && FOUND_ENV_KEYS+=("Anthropic")
[ -n "${OPENROUTER_API_KEY:-}" ] && FOUND_ENV_KEYS+=("OpenRouter")
[ -n "${GEMINI_API_KEY:-}" ] && FOUND_ENV_KEYS+=("Gemini")
[ -n "${GROQ_API_KEY:-}" ] && FOUND_ENV_KEYS+=("Groq")
[ -n "${DEEPSEEK_API_KEY:-}" ] && FOUND_ENV_KEYS+=("DeepSeek")
[ -n "${MISTRAL_API_KEY:-}" ] && FOUND_ENV_KEYS+=("Mistral")

# Detect local LLM binaries
FOUND_OLLAMA=false
FOUND_LLAMA_SERVER=false
command -v ollama &>/dev/null && FOUND_OLLAMA=true
command -v llama-server &>/dev/null && FOUND_LLAMA_SERVER=true

FORGE_TYPE="github"
GITEA_URL="https://gitea.example.com"
SELF_HOST_LLM=false
OLLAMA_MODEL=""
CLOUD_MODEL=""

if [ "${INTERACTIVE}" = true ]; then
  echo ""
  echo "------------------------------------------------------------"
  echo "  Step 1: Code Forge Configuration (GitHub / Gitea / Standalone)"
  echo "------------------------------------------------------------"
  echo "Do you want to configure a code forge to watch for issues/PRs?"
  echo "  1) GitHub (Default - recommended for GitHub repos)"
  echo "  2) Gitea  (Self-hosted Gitea instance)"
  echo "  3) None   (Run Archie in standalone mode without forge polling)"
  read -rp "Select option [1-3, default=1]: " forge_option || forge_option="1"
  forge_option="${forge_option:-1}"

  case "${forge_option}" in
    1)
      FORGE_TYPE="github"
      echo "  See docs/github-token.md for instructions (Note: Archie requires a Classic PAT, ghp_...)."
      read_secret "Enter ARCHIE_GITHUB_TOKEN (leave blank to configure later): " gh_token_input
      if [ -n "${gh_token_input}" ]; then
        set_env_key ARCHIE_GITHUB_TOKEN "${gh_token_input}"
      fi
      ;;
    2)
      FORGE_TYPE="gitea"
      read -rp "Enter Gitea Base URL [https://gitea.example.com]: " gitea_url_input || gitea_url_input=""
      GITEA_URL="${gitea_url_input:-https://gitea.example.com}"
      read_secret "Enter ARCHIE_GITEA_TOKEN (leave blank to configure later): " gitea_token_input
      if [ -n "${gitea_token_input}" ]; then
        set_env_key ARCHIE_GITEA_TOKEN "${gitea_token_input}"
      fi
      # Copy Gitea plugin scripts if available
      if [ -d "${SRC_DIR}/extras/gitea" ]; then
        cp -rn "${SRC_DIR}/extras/gitea/"* "${ARCHIE_DATA_DIR}/plugins/" 2>/dev/null || true
      fi
      ;;
    3)
      FORGE_TYPE="none"
      echo "  Forge polling disabled. Archie will run in standalone mode."
      ;;
  esac

  echo ""
  echo "------------------------------------------------------------"
  echo "  Step 2: LLM Provider Configuration"
  echo "------------------------------------------------------------"

  if [ "${FOUND_OLLAMA}" = true ] || [ "${FOUND_LLAMA_SERVER}" = true ]; then
    echo "Detected local LLM engines on your system:"
    [ "${FOUND_OLLAMA}" = true ] && echo "  - Ollama"
    [ "${FOUND_LLAMA_SERVER}" = true ] && echo "  - llama-server"
    echo ""
    read -rp "Do you want Archie to use a self-hosted local LLM? [y/N]: " self_host_choice || self_host_choice="n"
    if [[ "${self_host_choice}" =~ ^[Yy] ]]; then
      SELF_HOST_LLM=true
    fi
  else
    read -rp "Do you want to set up Archie with a self-hosted local LLM (Ollama / llama.cpp)? [y/N]: " self_host_choice || self_host_choice="n"
    if [[ "${self_host_choice}" =~ ^[Yy] ]]; then
      SELF_HOST_LLM=true
    fi
  fi

  if [ "${SELF_HOST_LLM}" = true ]; then
    if [ "${FOUND_OLLAMA}" = true ]; then
      echo "Querying installed Ollama models ('ollama list')..."
      OLLAMA_MODELS=($(ollama list 2>/dev/null | tail -n +2 | awk '{print $1}'))

      if [ ${#OLLAMA_MODELS[@]} -gt 0 ]; then
        echo "Installed Ollama models found:"
        for idx in "${!OLLAMA_MODELS[@]}"; do
          echo "  $((idx+1))) ${OLLAMA_MODELS[$idx]}"
        done
        read -rp "Select model number [1-${#OLLAMA_MODELS[@]}, default=1]: " model_idx || model_idx="1"
        model_idx="${model_idx:-1}"
        selected_i=$((model_idx - 1))
        if [ ${selected_i} -ge 0 ] && [ ${selected_i} -lt ${#OLLAMA_MODELS[@]} ]; then
          OLLAMA_MODEL="${OLLAMA_MODELS[$selected_i]}"
          echo "Selected model: ${OLLAMA_MODEL}"
        fi
      else
        echo "No models found in Ollama yet."
        read -rp "Enter model name to pull or use (e.g. llama3 or qwen2.5:7b): " OLLAMA_MODEL || OLLAMA_MODEL="llama3"
      fi
    else
      echo "Ollama is not currently found on \$PATH."
      echo "Install Ollama from https://ollama.com and pull a model (e.g. 'ollama pull llama3')."
      read -rp "Enter model name to configure (default: llama3): " OLLAMA_MODEL || OLLAMA_MODEL="llama3"
    fi
  fi

  # A cloud provider needs a model name. archied setup requires one, and the
  # installer previously emitted no [models] at all on this path.
  if [ "${SELF_HOST_LLM}" = false ]; then
    read -rp "Model name for OpenAI (e.g. gpt-5.4): " CLOUD_MODEL || CLOUD_MODEL=""
    CLOUD_MODEL="${CLOUD_MODEL:-gpt-5.4}"
  fi

  # Prompt for cloud provider key if not self-hosting and no keys detected
  if [ "${SELF_HOST_LLM}" = false ] && [ ${#FOUND_ENV_KEYS[@]} -eq 0 ]; then
    echo "No existing cloud provider API keys were detected."
    read_secret "Enter OPENAI_API_KEY (leave blank to configure later): " openai_key_input
    if [ -n "${openai_key_input}" ]; then
      set_env_key OPENAI_API_KEY "${openai_key_input}"
      FOUND_ENV_KEYS+=("OpenAI")
    fi
  fi
fi

# Generate config.toml through archied setup rather than here.
#
# The installer hand-rolling TOML already drifted from the schema once: the
# generated branch hardcoded the GitHub token key and the Gitea host, so choosing
# Gitea produced a config naming a token the installer never wrote. The durable
# fix, decided in docs/architecture/configuration.md, is that the code writing
# the config is the code that reads it -- archied setup renders it, and archied
# loads it.
#
# The answers collected above are passed as parameters. Secrets are passed as
# REFERENCES to the env keys set_env_key just wrote, never as values: a value on a
# command line lands in shell history and every process listing, and would be a
# second way to set a secret that no secret engine knows about.
if [ ! -f "${ARCHIE_CONFIG_DIR}/config.toml" ]; then
  echo "==> Generating initial config.toml..."
  setup_args=(--defaults -config "${ARCHIE_CONFIG_DIR}/config.toml" -forge-type "${FORGE_TYPE}")
  case "${FORGE_TYPE}" in
    github)
      setup_args+=(-forge-secret-ref env:ARCHIE_GITHUB_TOKEN)
      ;;
    gitea)
      setup_args+=(-forge-host "${GITEA_URL}" -forge-secret-ref env:ARCHIE_GITEA_TOKEN)
      ;;
  esac
  if [ "${SELF_HOST_LLM}" = true ]; then
    setup_args+=(-provider ollama -model "${OLLAMA_MODEL}")
  else
    # Reference the key in the env file, so the config keeps working if the
    # operator adds it later. With no key present archied disables that provider
    # and says so, rather than leaving the daemon unable to start.
    setup_args+=(-provider openai -model "${CLOUD_MODEL}" -provider-secret-ref env:OPENAI_API_KEY)
  fi
  if ! "${ARCHIE_BIN_DIR}/archied" setup "${setup_args[@]}"; then
    echo "ERROR: archied setup could not generate ${ARCHIE_CONFIG_DIR}/config.toml" >&2
    exit 1
  fi
else
  echo "  [SKIP] ${ARCHIE_CONFIG_DIR}/config.toml already exists (preserving user config)."
fi

# 6. Seed skills, personas, memories, and onboarding tasks
echo "==> Seeding skills and templates..."

# Copy built-in skills from .agents/skills and examples/skills
if [ -d "${SRC_DIR}/.agents/skills" ]; then
  cp -rf "${SRC_DIR}/.agents/skills/"* "${ARCHIE_CONFIG_DIR}/skills/" 2>/dev/null || true
fi
if [ -d "${SRC_DIR}/examples/skills" ]; then
  cp -rn "${SRC_DIR}/examples/skills/"* "${ARCHIE_CONFIG_DIR}/skills/" 2>/dev/null || true
fi

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
  # hand-written units before its own updater will run (archie-core-enow), and
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
  # this unit a host looks healthy while its channels are dead (archie-core-1c01).
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
  for unit in archied archie-state-store archie-gateway archie-ui archie-messaging; do
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
    systemctl --user enable --now archie-state-store archie-gateway archied archie-ui archie-messaging ||
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
echo "  - Binaries   : ${ARCHIE_BIN_DIR}/{archied,archie-state-store,archie-gateway,archie-ui,archie-messaging,archie-playbooks}"
echo "  - Agent image: ghcr.io/samcharles93/archie-agent:latest"
echo "  - Config     : ${ARCHIE_CONFIG_DIR}/config.toml"
echo "  - Secrets    : ${ENV_FILE}"
echo "  - Data       : ${ARCHIE_DATA_DIR}/"
echo "  - Forge Mode : ${FORGE_TYPE}"

if [ "${SELF_HOST_LLM}" = true ] && [ -n "${OLLAMA_MODEL}" ]; then
  echo "  - LLM Model  : ollama/${OLLAMA_MODEL}"
elif [ ${#FOUND_ENV_KEYS[@]} -gt 0 ]; then
  echo "  - LLM Keys   : Found (${FOUND_ENV_KEYS[*]})"
fi
echo ""

# Check PATH
if [[ ":$PATH:" != *":${ARCHIE_BIN_DIR}:"* ]]; then
  echo "NOTE: ${ARCHIE_BIN_DIR} is not in your current PATH."
  echo "Add it to your shell configuration file (~/.bashrc or ~/.zshrc):"
  echo "  export PATH=\"${ARCHIE_BIN_DIR}:\$PATH\""
  echo ""
fi

if [ "${SELF_HOST_LLM}" = false ] && [ ${#FOUND_ENV_KEYS[@]} -eq 0 ] && ! grep -q "=" "${ENV_FILE}" 2>/dev/null; then
  echo "============================================================"
  echo "  Notice: LLM Provider Configuration Needed"
  echo "============================================================"
  echo "No LLM provider keys or local models were configured yet."
  echo "Archie requires an LLM provider to run workflows."
  echo ""
  echo "To finish configuration:"
  echo "  1. For Cloud Providers (OpenAI, Anthropic, OpenRouter, etc.):"
  echo "     Add your API key to ${ENV_FILE}:"
  echo "       OPENAI_API_KEY=\"sk-...\""
  echo "  2. For Local LLMs (Ollama):"
  echo "     Install Ollama (https://ollama.com), run 'ollama pull llama3',"
  echo "     and set [models] in ${ARCHIE_CONFIG_DIR}/config.toml to 'ollama/llama3'."
  echo ""
  echo "See documentation: ${SRC_DIR}/docs/github-token.md"
  echo "============================================================"
  echo ""
fi

if [ "${SERVICE_INSTALLED}" = true ] && [ "${AUTO_START}" = true ]; then
  echo "Service Management:"
  echo "  - Units installed   : archied archie-state-store archie-gateway archie-ui archie-messaging"
  echo "  - View live logs    : journalctl --user -u archied -f"
  echo "  - Service status    : systemctl --user status archied archie-state-store archie-gateway archie-ui archie-messaging"
  echo "  - Restart daemon    : systemctl --user restart archied"
else
  echo "Manual Startup:"
  echo "  1. Add tokens to ${ENV_FILE} or ${ARCHIE_CONFIG_DIR}/config.toml"
  echo "  2. Run daemon       : archied"
fi
echo ""
