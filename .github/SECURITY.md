# Security policy

## Supported versions

Security fixes are applied to `main` and the latest tagged release. Before the first
stable release, users should run the newest tagged prerelease or build from `main`.

## Reporting a vulnerability

Do not disclose a suspected vulnerability in a public issue, discussion, pull request,
or log attachment.

Use the repository **Security** tab and choose **Report a vulnerability** to start a
private security advisory. Include the affected version, impact, reproduction steps,
and a minimal proof of concept with all credentials and infrastructure identifiers
removed. If private reporting is not available, contact the repository owner privately
through their GitHub profile before sharing technical details.

The maintainer will acknowledge a complete report, assess severity, coordinate a fix,
and publish an advisory when affected users have a safe upgrade path. Please allow a
reasonable remediation window before public disclosure.

For the project's security architecture and deployment assumptions, see
[`docs/SECURITY.md`](../docs/SECURITY.md).
