#!/usr/bin/env bash
set -euo pipefail

REPO_URL="${MEOW_REPO_URL:-https://github.com/Riffee/meow-zond.git}"
TARGET="${MEOW_DIR:-/opt/meow-zond}"
TTY=/dev/tty

die() {
  echo "Ошибка: $*" >&2
  exit 1
}

have_tty() {
  [[ -r "$TTY" ]]
}

ask() {
  local prompt="$1" default="${2:-}" value=""
  if have_tty; then
    printf '%s%s: ' "$prompt" "${default:+ [$default]}" >&2
    IFS= read -r value < "$TTY" || true
  fi
  printf '%s' "${value:-$default}"
}

confirm() {
  local answer=""
  if have_tty; then
    printf '%s [Y/n]: ' "$1" >&2
    IFS= read -r answer < "$TTY" || true
  fi
  [[ -z "$answer" || "$answer" =~ ^[YyДд] ]]
}

env_value() {
  local key="$1" line
  line="$(grep -m1 "^${key}=" .env 2>/dev/null || true)"
  [[ -n "$line" ]] || return 0
  line="${line#*=}"
  if [[ "$line" == \"*\" && "$line" == *\" ]]; then
    line="${line:1:${#line}-2}"
  elif [[ "$line" == \'*\' && "$line" == *\' ]]; then
    line="${line:1:${#line}-2}"
  fi
  printf '%s' "$line"
}

[[ "$(id -u)" -eq 0 ]] || die "запустите установщик через sudo"

script_dir=""
if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
  script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd || true)"
fi
if [[ -n "$script_dir" && -f "$script_dir/compose.yaml" && -f "$script_dir/Dockerfile" ]]; then
  TARGET="$script_dir"
else
  if ! command -v git >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
      echo "Устанавливаю git…"
      apt-get update -qq
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq git
    else
      die "не найден git; установите git и повторите"
    fi
  fi
  if [[ -d "$TARGET/.git" ]]; then
    echo "Обновляю $TARGET…"
    git -C "$TARGET" pull --ff-only
  elif [[ -e "$TARGET" ]]; then
    die "$TARGET уже существует и не является git-репозиторием; выберите MEOW_DIR или уберите каталог"
  else
    mkdir -p "$(dirname "$TARGET")"
    echo "Клонирую $REPO_URL в $TARGET…"
    git clone --depth 1 "$REPO_URL" "$TARGET"
  fi
fi
cd "$TARGET"

if ! command -v docker >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1 && confirm "Docker не найден. Установить Docker Engine через get.docker.com?"; then
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker >/dev/null 2>&1 || true
  else
    die "установите Docker и Docker Compose V2, затем повторите"
  fi
fi
if ! docker compose version >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    echo "Устанавливаю Docker Compose plugin…"
    apt-get update -qq || true
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker-compose-plugin || true
  fi
fi
docker compose version >/dev/null 2>&1 || die "нужен Docker Compose V2 (docker compose)"
docker info >/dev/null 2>&1 || die "Docker daemon недоступен"

server="${MEOW_SERVER:-}"
name="${MEOW_PROBE_NAME:-}"
image=""
if [[ -f .env ]]; then
  [[ -n "$server" ]] || server="$(env_value MEOW_SERVER)"
  [[ -n "$name" ]] || name="$(env_value MEOW_PROBE_NAME)"
  image="$(env_value MEOW_ZOND_IMAGE)"
fi

[[ -n "$server" ]] || server="$(ask "Адрес статус-страницы (https://...)" "")"
if [[ -z "$name" ]]; then
  host_default="$(hostname -s 2>/dev/null || echo server)"
  name="$(ask "Имя зонда" "meow-zond-$host_default")"
fi

server="$(printf '%s' "$server" | tr -d '\r\n')"
name="$(printf '%s' "$name" | tr -d '\r\n')"
case "$server" in
  https://*|http://*) ;;
  *) die "адрес должен начинаться с https:// (http:// только для локальной отладки)" ;;
esac
[[ "$server" != *[[:space:]]* ]] || die "адрес не должен содержать пробелы"
[[ ${#name} -le 100 && "$name" != *"'"* ]] || die "имя: до 100 символов и без одинарной кавычки"

case "$server" in
  http://*) echo "Внимание: HTTP без шифрования допустим только для локальной отладки." >&2 ;;
esac

umask 077
{
  printf 'MEOW_SERVER=%s\n' "$server"
  printf "MEOW_PROBE_NAME='%s'\n" "$name"
  # A host that runs the published image instead of building it here must not
  # lose that choice when the installer rewrites the file.
  if [[ -n "$image" ]]; then
    printf 'MEOW_ZOND_IMAGE=%s\n' "$image"
  fi
} > .env
chmod 600 .env

if [[ -n "$image" ]]; then
  echo "Обновляю опубликованный образ ${image}…"
  docker compose pull
  docker compose up -d
else
  echo "Собираю и запускаю контейнер meow-zond…"
  docker compose up -d --build
fi

sleep 2
docker compose ps
echo
echo "Логи:"
echo "  docker compose logs -f meow-zond"
echo "Последние 500 строк кольцевого лога:"
echo "  docker compose exec meow-zond tail -n 500 /data/probe.log"
