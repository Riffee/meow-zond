# meow-zond

Открытый агент внешних проверок для meow! status. Запускается в
Docker-контейнере `meow-zond`, выполняет TCP, ICMP, HTTP, DNS, TLS и
VPN end-to-end проверки и отправляет результаты на статус-страницу.

## Быстрый старт на Linux-сервере

1. В админке откройте **Зонды → + Зонд**, задайте имя и скопируйте одноразовый
   код подключения.
2. Склонируйте репозиторий и запустите установщик:

```bash
sudo bash -c 'git clone https://github.com/Riffee/meow-zond.git /opt/meow-zond \
  && cd /opt/meow-zond \
  && ./install.sh --server https://status.example.com --code ABCD-EFGH-JKLM --name "Server zond"'
```

Без аргументов установщик спросит адрес, код и имя. Параметры можно передать
переменными `MEOW_SERVER`, `MEOW_CODE`, `MEOW_PROBE_NAME`.

Установщик собирает образ, запускает контейнер `meow-zond` и пишет `.env`
с правами `0600`. Код подключения одноразовый: после регистрации зонд хранит
свою конфигурацию в volume `meow-zond-data` и перезапускается без повторного
кода.

## Требования

- Linux-хост с Docker и Docker Compose V2.
- `network_mode: host`: проверки и определение сети должны идти через
  физическую сеть хоста.
- `NET_RAW` для ICMP и привязки к интерфейсу. Остальные capabilities сброшены,
  корневая ФС контейнера read-only, запись разрешена только в `/data` и `/tmp`.
- Для VPN end-to-end нужен отдельно проверенный Xray; в репозиторий и образ
  он не входит. Укажите `xray_path` и `xray_sha256` в `/data/probe.json`.

## Логи

```bash
docker compose logs -f meow-zond
docker compose exec meow-zond tail -n 500 /data/probe.log
```

Каждая проверка пишет одну строку: имя монитора, тип, цель, результат,
этап ошибки и задержку. Секреты, VLESS UUID и Reality-параметры не логируются.

- `/data/probe.log` — кольцевой буфер ровно на **500 последних строк**;
- Docker JSON-лог ограничен **64 КБ и одним файлом** (`max-size`, `max-file`).

Обе копии ограничены, поэтому логи не растут бесконечно.

## Обновление и повторная регистрация

```bash
# обновить исходники и пересобрать контейнер
git pull
docker compose up -d --build

# удалить конфигурацию и зарегистрировать заново
docker compose down -v
./install.sh --server https://status.example.com --code NEW-CODE --name "Server zond"
```

`docker compose down` без `-v` сохраняет конфигурацию и историю зонда.

## Безопасность

- `.env` содержит одноразовый код и не должен попадать в Git — он уже в
  `.gitignore`; не публикуйте файл.
- Контейнер не требует входящих портов и публичного доступа.
- HTTP-адрес сервера разрешён только для локальной отладки, в production
  используйте HTTPS.
- Установщик проверяет только адрес и код; Xray и тестовые VPN-профили
  настраиваются отдельно.

## Структура

```text
cmd/meow-zond      консольный/headless агент и Windows-служба
internal/probe     сетевые проверки, Xray и кольцевой spool
internal/model     общие типы монитора, задания и результата
internal/secure    идентификаторы и хеши
internal/distribution  встроенная конфигурация для скачиваемых сборок
```

Тесты:

```bash
go test ./...
```

## Лицензия

MIT. См. [LICENSE](LICENSE).
