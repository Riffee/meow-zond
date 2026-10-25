<div align="center">
  <img src="assets/logo.png" alt="meow-zond" width="180">
  <h1>meow-zond</h1>
  <p>Открытый Docker-зонд для meow! status: реальные сетевые, веб- и VPN-проверки из вашей сети.</p>
  <p>
    <img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="MIT License">
    <img src="https://img.shields.io/badge/Language-Bash-4EAA25.svg?logo=gnu-bash&logoColor=white" alt="Bash">
    <img src="https://img.shields.io/badge/Tech-Docker-2496ED.svg?logo=docker&logoColor=white" alt="Docker">
    <img src="https://img.shields.io/badge/Release-v1.0.0-blueviolet.svg" alt="Version">
  </p>
  <p><a href="README.md">English</a> / <strong>Русский</strong></p>
</div>

## Установка одной командой

```bash
curl -fsSL https://raw.githubusercontent.com/Riffee/meow-zond/main/install.sh | sudo bash
```

Установщик клонирует репозиторий в `/opt/meow-zond`, при необходимости
установит Docker, спросит адрес статус-страницы и имя зонда, затем соберёт и
запустит контейнер `meow-zond`. Коды и ключи подключения не нужны.

Скрипт: [install.sh](https://raw.githubusercontent.com/Riffee/meow-zond/main/install.sh)

## Открытая регистрация

Любой, кто знает адрес статус-страницы, может зарегистрировать зонд по имени.
Это сделано намеренно: зонд — независимая точка измерений, а не
привилегированный пользователь. Регистрация ограничена по частоте и общему
количеству зондов; назначение зондов мониторам и удаление нежелательных
зондов остаются за администратором.

## Что делает

- Выполняет TCP, ICMP, HTTP, DNS и TLS-проверки из сети зонда.
- Поддерживает опциональные Xray/VLESS/REALITY end-to-end проверки. Xray не
  входит в образ: укажите `xray_path` и `xray_sha256` в `/data/probe.json`
  для отдельно проверенного бинарника.
- Отправляет на статус-страницу только исходящие HTTPS-запросы. Входящие
  порты не нужны.
- Использует `network_mode: host`, чтобы проверки и определение сети шли через
  реальный интернет-путь хоста. Для ICMP и привязки к интерфейсу нужен
  `NET_RAW`; остальные capabilities сброшены, корневая ФС read-only.
- Хранит идентификатор зонда, очередь результатов и настройки в volume
  `meow-zond-data`.

## Логи

```bash
docker compose logs -f meow-zond
docker compose exec meow-zond tail -n 500 /data/probe.log
```

Каждая проверка пишет имя монитора, тип, цель, результат, этап ошибки и
задержку. Секреты, VLESS UUID и Reality-параметры не логируются.

- `/data/probe.log` — кольцевой буфер ровно на **500** последних строк;
- Docker JSON-лог ограничен **64 КБ** и одним ротированным файлом.

## Требования

- Linux-хост с root/sudo.
- Docker Engine и Docker Compose V2. Установщик может поставить Docker.
- Исходящий интернет и HTTPS-доступ к статус-странице.

## Обновление и повторная установка

```bash
cd /opt/meow-zond
git pull
docker compose up -d --build
```

Сбросить конфигурацию зонда и зарегистрироваться заново:

```bash
cd /opt/meow-zond
docker compose down -v
./install.sh
```

`docker compose down` без `-v` сохраняет volume и идентификатор зонда.

## Безопасность

- Зонд получает только типизированные задания проверок и возвращает
  результаты; админ-пароль и токен Remnawave ему недоступны.
- Открытая регистрация означает, что новый зонд может появиться без
  предварительного одобрения. Не публикуйте адрес статус-страницы, если не
  хотите публичных зондов; лишние зонды удаляются в админке.
- В production используйте HTTPS. HTTP разрешён только для локальной
  отладки.

## Структура

```text
cmd/meow-zond          консольный/headless агент и Windows-служба
internal/probe         сетевые проверки, Xray и spool
internal/model         общие типы монитора, задания и результата
internal/secure        идентификаторы и хеши
internal/distribution  встроенный адрес сервера для скачиваемых сборок
```

Тесты:

```bash
go test ./...
```

## Лицензия

MIT. См. [LICENSE](LICENSE).
