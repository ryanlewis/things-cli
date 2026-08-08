# Security Policy

## Supported versions

Only the [latest release](https://github.com/ryanlewis/things-cli/releases/latest) is supported with security fixes.

## Reporting a vulnerability

Please report vulnerabilities privately via [GitHub Security Advisories](https://github.com/ryanlewis/things-cli/security/advisories/new) — do not open a public issue. You should get a response within a week.

## Verifying releases

Release artifacts are checksummed (`checksums.txt`, verified automatically by `install.sh`) and carry [build provenance attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations):

```sh
gh attestation verify things_<version>_darwin_arm64.tar.gz -R ryanlewis/things-cli
```
