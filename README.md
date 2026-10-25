<div align="center">
  <img src="assets/logo.png" alt="meow-zond" width="180">
  <h1>meow-zond</h1>
  <p>Self-hosted Docker probe for meow! status — real network, web and VPN checks from your own network.</p>
  <p>
    <img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="MIT License">
    <img src="https://img.shields.io/badge/Language-Bash-4EAA25.svg?logo=gnu-bash&logoColor=white" alt="Bash">
    <img src="https://img.shields.io/badge/Tech-Docker-2496ED.svg?logo=docker&logoColor=white" alt="Docker">
    <img src="https://img.shields.io/badge/Release-v1.0.0-blueviolet.svg" alt="Version">
  </p>
  <p><strong>English</strong> / <a href="README.ru.md">Русский</a></p>
</div>

## Install with one command

```bash
curl -fsSL https://raw.githubusercontent.com/Riffee/meow-zond/main/install.sh | sudo bash
```

The installer clones the repository into `/opt/meow-zond`, installs Docker if
needed, asks for your status page URL and a probe name, then builds and starts
the `meow-zond` container. No pairing codes or registration keys.

Script: [install.sh](https://raw.githubusercontent.com/Riffee/meow-zond/main/install.sh)

## Open registration

Anyone who can reach the status page can register a probe by name. This is
intentional: probes are independent measurement points, not privileged users.
Registration is rate-limited and capped; the administrator decides which probes
are assigned to monitors and can delete unwanted probes.

## What it does

- Runs TCP, ICMP, HTTP, DNS and TLS checks from the probe network.
- Supports optional Xray/VLESS/REALITY end-to-end checks. Xray is not bundled;
  point `xray_path` and `xray_sha256` in `/data/probe.json` to a verified binary.
- Sends only outbound HTTPS requests to the status page. No inbound ports.
- Uses `network_mode: host` so checks and network detection use the host's real
  internet path. ICMP and interface binding use `NET_RAW`; other capabilities
  are dropped and the container filesystem is read-only.
- Keeps the probe identity, spool and settings in the `meow-zond-data` volume.

## Logs

```bash
docker compose logs -f meow-zond
docker compose exec meow-zond tail -n 500 /data/probe.log
```

Every check logs the monitor name, check type, target, outcome, failure stage
and latency. Secrets, VLESS UUIDs and Reality parameters are never logged.

- `/data/probe.log` is a ring buffer of exactly the last **500** lines;
- the Docker JSON log is capped at **64 KB** in one rotated file.

## Requirements

- Linux host with root/sudo.
- Docker Engine and Docker Compose V2. The installer offers to install Docker.
- Outbound internet access and HTTPS access to the status page.

## Update or reinstall

```bash
cd /opt/meow-zond
git pull
docker compose up -d --build
```

Reset the probe configuration and register again:

```bash
cd /opt/meow-zond
docker compose down -v
./install.sh
```

`docker compose down` without `-v` keeps the volume and probe identity.

## Security

- The probe only receives typed check tasks and returns results; it never gets
  admin credentials or the Remnawave token.
- Open registration means a new probe can appear without prior approval. Keep
  the status page URL private if you do not want public probes, or remove
  unwanted probes from the admin panel.
- Use HTTPS for the status page in production. HTTP is accepted only for local
  development.

## Source layout

```text
cmd/meow-zond          console/headless agent and Windows service
internal/probe         network checks, Xray runner and spool
internal/model         shared monitor, task and result types
internal/secure        identifiers and hashes
internal/distribution  embedded server configuration for downloaded builds
```

Tests:

```bash
go test ./...
```

## License

MIT. See [LICENSE](LICENSE).
