#!/usr/bin/env bash
set -euo pipefail

REPO_SLUG="SatanaKonst/php-fpm-process-exporter"
BIN_NAME="php-fpm-process-exporter"
SERVICE_NAME="php-fpm-process-exporter.service"
CONFIG_PATH="/etc/php-fpm-process-exporter.json"
BIN_DIR="/usr/local/bin"
SERVICE_DIR="/etc/systemd/system"

LISTEN_ADDR=":9254"
PROC_ROOT="/proc"
INCLUDE_THREADS="false"
HEALTH_PATH="/healthz"
METRICS_PATH="/metrics"
BASIC_AUTH_USER=""
BASIC_AUTH_PASS=""
INSTALL_DIR="$BIN_DIR"
WORKDIR=""

usage() {
  cat <<'EOF'
Usage: install_ubuntu.sh [options]

Options:
  --listen ADDR           HTTP listen address (default: :9254)
  --proc-root DIR         proc filesystem root (default: /proc)
  --include-threads       Export per-thread CPU metrics
  --health-path PATH      Health endpoint path (default: /healthz)
  --metrics-path PATH     Metrics endpoint path (default: /metrics)
  --basic-auth-user USER  Enable basic auth with username
  --basic-auth-pass PASS  Enable basic auth with password
  --install-dir DIR       Binary install directory (default: /usr/local/bin)
  --workdir DIR           Temporary working directory
  -h, --help              Show help

Example:
  sudo ./install_ubuntu.sh \
    --listen :9255 \
    --basic-auth-user metrics \
    --basic-auth-pass secret

If basic auth flags are omitted, the installer will ask interactively whether to enable it.
EOF
}

do_root() {
  if [[ $EUID -eq 0 ]]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    echo "Need root or sudo to run: $*" >&2
    exit 1
  fi
}

ensure_cmd() {
  local cmd="$1"
  local pkg="${2:-$1}"
  if command -v "$cmd" >/dev/null 2>&1; then
    return 0
  fi

  if command -v apt-get >/dev/null 2>&1; then
    do_root apt-get update
    do_root apt-get install -y "$pkg"
    return 0
  fi

  echo "Missing required command: $cmd" >&2
  exit 1
}

prompt_from_tty() {
  local prompt="$1"
  local __varname="$2"
  local value=""
  if [[ -t 0 ]]; then
    read -r -p "$prompt" value
  else
    read -r -p "$prompt" value </dev/tty
  fi
  printf -v "$__varname" '%s' "$value"
}

prompt_secret() {
  local prompt="$1"
  local __varname="$2"
  local value=""
  if [[ -t 0 ]]; then
    read -r -s -p "$prompt" value
    echo
  else
    read -r -s -p "$prompt" value </dev/tty
    printf '\n' >/dev/tty
  fi
  printf -v "$__varname" '%s' "$value"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --listen)
      LISTEN_ADDR="${2:?missing value for --listen}"
      shift 2
      ;;
    --proc-root)
      PROC_ROOT="${2:?missing value for --proc-root}"
      shift 2
      ;;
    --include-threads)
      INCLUDE_THREADS="true"
      shift
      ;;
    --health-path)
      HEALTH_PATH="${2:?missing value for --health-path}"
      shift 2
      ;;
    --metrics-path)
      METRICS_PATH="${2:?missing value for --metrics-path}"
      shift 2
      ;;
    --basic-auth-user)
      BASIC_AUTH_USER="${2:?missing value for --basic-auth-user}"
      shift 2
      ;;
    --basic-auth-pass)
      BASIC_AUTH_PASS="${2:?missing value for --basic-auth-pass}"
      shift 2
      ;;
    --install-dir)
      INSTALL_DIR="${2:?missing value for --install-dir}"
      shift 2
      ;;
    --workdir)
      WORKDIR="${2:?missing value for --workdir}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  if [[ "${ID:-}" != "ubuntu" ]]; then
    echo "This installer is intended for Ubuntu; detected ID=${ID:-unknown}" >&2
  fi
fi

if [[ -z "$WORKDIR" ]]; then
  WORKDIR="$(mktemp -d)"
fi

if [[ -z "$BASIC_AUTH_USER" && -z "$BASIC_AUTH_PASS" ]]; then
  local_enable_auth=""
  prompt_from_tty "Enable basic auth? [y/N]: " local_enable_auth
  case "${local_enable_auth,,}" in
    y|yes)
      prompt_from_tty "Basic auth username: " BASIC_AUTH_USER
      while [[ -z "$BASIC_AUTH_USER" ]]; do
        prompt_from_tty "Username cannot be empty. Basic auth username: " BASIC_AUTH_USER
      done
      prompt_secret "Basic auth password: " BASIC_AUTH_PASS
      while [[ -z "$BASIC_AUTH_PASS" ]]; do
        prompt_secret "Password cannot be empty. Basic auth password: " BASIC_AUTH_PASS
      done
      ;;
  esac
elif [[ -n "$BASIC_AUTH_USER" && -z "$BASIC_AUTH_PASS" ]]; then
  prompt_secret "Basic auth password: " BASIC_AUTH_PASS
  while [[ -z "$BASIC_AUTH_PASS" ]]; do
    prompt_secret "Password cannot be empty. Basic auth password: " BASIC_AUTH_PASS
  done
elif [[ -z "$BASIC_AUTH_USER" && -n "$BASIC_AUTH_PASS" ]]; then
  prompt_from_tty "Basic auth username: " BASIC_AUTH_USER
  while [[ -z "$BASIC_AUTH_USER" ]]; do
    prompt_from_tty "Username cannot be empty. Basic auth username: " BASIC_AUTH_USER
  done
fi

ensure_cmd curl curl

detect_os_arch() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "$os" in
    linux) ;;
    *)
      echo "Unsupported OS: $os" >&2
      exit 1
      ;;
  esac

  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *)
      echo "Unsupported architecture: $arch" >&2
      exit 1
      ;;
  esac

  printf '%s %s\n' "$os" "$arch"
}

download_release_binary() {
  local os arch tag version asset_name url tmpfile
  read -r os arch < <(detect_os_arch)
  tag="$(curl -fsSL "https://api.github.com/repos/${REPO_SLUG}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  if [[ -z "$tag" ]]; then
    echo "Could not determine latest release tag for ${REPO_SLUG}" >&2
    return 1
  fi

  version="${tag#v}"
  asset_name="${BIN_NAME}_${version}_${os}_${arch}"
  url="https://github.com/${REPO_SLUG}/releases/download/${tag}/${asset_name}"
  tmpfile="$WORKDIR/$asset_name"

  echo "Downloading ${asset_name}"
  curl -fsSL -o "$tmpfile" "$url"
  install -m 0755 "$tmpfile" "$WORKDIR/$BIN_NAME"
}

if ! download_release_binary; then
  echo "Release binary download failed. Check that the GitHub release exists and contains ${BIN_NAME} for your platform." >&2
  exit 1
fi

if [[ -n "${SUDO_USER:-}" && $EUID -eq 0 ]]; then
  chown "${SUDO_USER}:${SUDO_USER}" "$WORKDIR/$BIN_NAME" 2>/dev/null || true
fi

do_root install -d "$INSTALL_DIR"
do_root install -m 0755 "$WORKDIR/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"

cat > "$WORKDIR/$BIN_NAME.json" <<EOF
{
  "listen": "$LISTEN_ADDR",
  "proc_root": "$PROC_ROOT",
  "include_threads": $INCLUDE_THREADS,
  "health_path": "$HEALTH_PATH",
  "metrics_path": "$METRICS_PATH"$(if [[ -n "$BASIC_AUTH_USER" ]]; then printf ',\n  "basic_auth": {\n    "username": "%s",\n    "password": "%s"\n  }' "$(printf '%s' "$BASIC_AUTH_USER" | sed 's/\\/\\\\/g; s/"/\\"/g')" "$(printf '%s' "$BASIC_AUTH_PASS" | sed 's/\\/\\\\/g; s/"/\\"/g')"; fi)
}
EOF

if [[ $EUID -eq 0 ]]; then
  install -m 0644 "$WORKDIR/$BIN_NAME.json" "$CONFIG_PATH"
else
  do_root install -m 0644 "$WORKDIR/$BIN_NAME.json" "$CONFIG_PATH"
fi

cat > "$WORKDIR/$SERVICE_NAME" <<EOF
[Unit]
Description=PHP-FPM process exporter
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$INSTALL_DIR/$BIN_NAME --config $CONFIG_PATH
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

if [[ $EUID -eq 0 ]]; then
  install -m 0644 "$WORKDIR/$SERVICE_NAME" "$SERVICE_DIR/$SERVICE_NAME"
  systemctl daemon-reload
  systemctl enable --now "$SERVICE_NAME"
else
  do_root install -m 0644 "$WORKDIR/$SERVICE_NAME" "$SERVICE_DIR/$SERVICE_NAME"
  do_root systemctl daemon-reload
  do_root systemctl enable --now "$SERVICE_NAME"
fi

echo "Installed $BIN_NAME to $INSTALL_DIR/$BIN_NAME"
echo "Config written to $CONFIG_PATH"
echo "Service installed as $SERVICE_NAME"
