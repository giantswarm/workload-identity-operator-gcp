# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Service account reconciler that creates a `Secret` with the needed `GOOGLE_APPLICATION_CREDENTIALS` json

### Changed

- Move `GOOGLE_APPLICATION_CREDENTIALS` from a `ConfigMap` to a `Secret`


## [0.1.0] - 2022-06-14

[Unreleased]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/giantswarm/workload-identity-operator-gcp/releases/tag/v0.1.0
