#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

usage() {
  cat <<'EOF'
Запуск зонда meow-zond в Docker.

  ./install.sh --server https://status.example.com --code ABCD-EFGH-JKLM --name "Server zond"

Код подключения создаётся в админке: Зонды → + Зонд. Он одноразовый.
После первой регистрации конфигурация хранится в volume meow-zond-data,
и повторный запуск продолжает работу с тем же зондом.

Параметры также можно задать через MEOW_SERVER, MEOW_CODE, MEOW_PROBE_NAME.
EOF
}

server="${MEOW_SERVER:-}"
code="${MEOW_CODE:-}"
name="${MEOW_PROBE_NAME:-}"

while (($#)); do
  case "$1" in
    --server)
      server="${2:-}"
      shift 2
      ;;
    --code)
      code="${2:-}"
      shift 2
      ;;
    --name)
      name="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Неизвестный аргумент: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

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

if [[ -f .env ]]; then
  [[ -z "$server" ]] && server="$(env_value MEOW_SERVER)"
  [[ -z "$code" ]] && code="$(env_value MEOW_CODE)"
  [[ -z "$name" ]] && name="$(env_value MEOW_PROBE_NAME)"
fi

if [[ -z "$server" && -t 0 ]]; then
  read -rp "Адрес статус-страницы (https://...): " server
fi
if [[ -z "$code" && -t 0 ]]; then
  read -rsp "Одноразовый код из админки: " code
  echo
fi
if [[ -z "$name" ]]; then
  if [[ -t 0 ]]; then
    read -rp "Имя зонда [meow-zond]: " name
  fi
  name="${name:-meow-zond}"
fi

if [[ -z "$server" || -z "$code" ]]; then
  echo "Нужны --server и --code. Код создаётся в админке: Зонды → + Зонд." >&2
  exit 1
fi
case "$server" in
  https://*|http://*) ;;
  *)
    echo "Адрес должен начинаться с https:// (http:// только для локальной отладки)." >&2
    exit 1
    ;;
esac
if [[ "$server" =~ [[:space:]] || "$code" =~ [[:space:]] ]]; then
  echo "Адрес и код не должны содержать пробелы." >&2
  exit 1
fi
code="$(printf '%s' "$code" | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')"
if [[ ${#name} -gt 100 || "$name" == *"'"* || "$name" == *$'\n'* || "$name" == *$'\r'* ]]; then
  echo "Имя зонда: до 100 символов, без одинарных кавычек и переводов строк." >&2
  exit 1
fi

command -v docker >/dev/null 2>&1 || {
  echo "Docker не установлен." >&2
  exit 1
}
docker compose version >/dev/null 2>&1 || {
  echo "Нужен Docker Compose V2 (команда docker compose)." >&2
  exit 1
}
docker info >/dev/null 2>&1 || {
  echo "Docker daemon недоступен. Запустите Docker или выполните установку через sudo." >&2
  exit 1
}

case "$server" in
  http://*)
    echo "Внимание: HTTP без шифрования допустим только для локальной отладки." >&2
    ;;
esac

umask 077
{
  printf 'MEOW_SERVER=%s\n' "$server"
  printf 'MEOW_CODE=%s\n' "$code"
  printf "MEOW_PROBE_NAME='%s'\n" "$name"
} > .env
chmod 600 .env

echo "Собираю и запускаю контейнер meow-zond…"
docker compose up -d --build

sleep 2
docker compose ps
echo
echo "Логи:"
echo "  docker compose logs -f meow-zond"
echo "Последние 500 строк кольцевого лога:"
echo "  docker compose exec meow-zond tail -n 500 /data/probe.log"
