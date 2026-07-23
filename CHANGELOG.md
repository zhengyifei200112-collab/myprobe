# Changelog

All notable changes to MyProbe are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases
use [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Node creation and Agent token rotation now show a ready-to-copy Linux one-click
  Agent installation command using the current Server URL.

### Changed

- Increased public dashboard typography and badge sizing for readability, and replaced
  platform-dependent flag emoji with bundled country flag artwork.

## [0.2.1] - 2026-07-22

### Changed

- Fresh Server installations and the default Compose configuration now expose
  `http://SERVER_IP:25775` directly, while an explicit reverse-proxy mode retains
  loopback binding and HTTPS-only session cookies.

## [0.2.0] - 2026-07-22

### Added

- A dedicated Agent container image and Linux host-monitoring Compose template now
  expose host metrics through read-only mounts while retaining outbound-only Agent
  communication.

### Fixed

- Agent containers no longer inherit the Server `/healthz` check, include the `ping`
  utility required by Ping tasks, and report logical host mount paths instead of the
  container bind-mount prefix.

## [0.1.1] - 2026-07-22

### Fixed

- Docker release builds now copy Vite output from the configured embedded asset path,
  with a pull-request container build preventing regressions before a release tag.

## [0.1.0] - 2026-07-22

### Added

- Multi-path deployment with a checksum-verifying Linux one-click installer for Server
  and Agent, lifecycle commands, published GHCR images, binary instructions, and
  Chinese installation documentation.
- Complete Simplified Chinese README covering features, deployment, configuration,
  security, development, and maintenance workflows.
- Repository governance baseline for GitHub Flow, Conventional Commits, code ownership,
  structured issues and pull requests, dependency updates, security reporting, and
  repeatable releases.
- Original Go Server and Agent, Vue dashboard, SQLite persistence, realtime monitoring,
  historical rollups, administration, alerts, sharing, maintenance, and deployment
  packaging.

[Unreleased]: https://github.com/zhengyifei200112-collab/myprobe/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/zhengyifei200112-collab/myprobe/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/zhengyifei200112-collab/myprobe/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/zhengyifei200112-collab/myprobe/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/zhengyifei200112-collab/myprobe/releases/tag/v0.1.0
