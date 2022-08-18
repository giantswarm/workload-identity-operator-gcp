# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2022-08-18

### Added

- Service account reconciler that creates a `Secret` with the needed `GOOGLE_APPLICATION_CREDENTIALS` json

### Changed

- Move `GOOGLE_APPLICATION_CREDENTIALS` from a `ConfigMap` to a `Secret`

## [0.2.0] - 2022-07-19

### Changed

- Don't push to gcp collection.
- Push app to default catalog instead of control plane.

## [0.1.0] - 2022-06-14

[Unreleased]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/giantswarm/workload-identity-operator-gcp/releases/tag/v0.1.0
