# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Add `global.podSecurityStandards.enforced` value for PSS migration.

## [0.5.0] - 2022-10-11

### Removed

- GCP Cluster Reconciler. That responsibility has been moved to fleet-membership-operator-gcp

## [0.4.3] - 2022-10-03

### Fixed

- Do not get ServiceAccount when mutating pod. The annotations on the service account are no longer needed for the webhook

## [0.4.2] - 2022-09-27

### Added

- Flag to enable cluster reconciler. Disabled by default. It should only be enabled when running on a Management Cluster.

## [0.4.1] - 2022-09-23

### Changed

- Updated documentation
- Increase container memory resource requests and limits

## [0.4.0] - 2022-09-21

### Added

- GCP cluster reconciler that creates a `secret` with the details of the workload identity membership on a workload cluster

## [0.3.2] - 2022-08-23

### Fixed 

- Fix wrong path to the Kubernetes ServiceAccount token in the google application credentials json

## [0.3.1] - 2022-08-19

### Fixed

- Add missing ingress to NetworkPolicy

### Changed

- Add `webhookPort` helm value

## [0.3.0] - 2022-08-18

### Added

- Service account reconciler that creates a `Secret` with the needed `GOOGLE_APPLICATION_CREDENTIALS` json

### Changed

- Move `GOOGLE_APPLICATION_CREDENTIALS` from a `ConfigMap` to a `Secret`

### Fixed
- Use Namespace value from request in webhook instead of the Pod definition. Fixes a bug where pods created by controllers like ReplicaSets don't get allowed because of a missing Namespace.

## [0.2.0] - 2022-07-19

### Changed

- Don't push to gcp collection.
- Push app to default catalog instead of control plane.

## [0.1.0] - 2022-06-14

[Unreleased]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.4.3...v0.5.0
[0.4.3]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/giantswarm/workload-identity-operator-gcp/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/giantswarm/workload-identity-operator-gcp/releases/tag/v0.1.0
