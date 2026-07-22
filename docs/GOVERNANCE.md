# Repository governance

## Visibility and branch protection

The repository is public and `main` is protected. GitHub Free provides branch protection
and repository rulesets for public repositories; a future change back to private would
require GitHub Pro, Team, or Enterprise to preserve these controls.

The active `main` protection requires:

- require a pull request before merging;
- require the `test` and `pull-request-policy` status checks;
- require all review conversations to be resolved;
- dismiss stale approvals after new commits;
- zero approvals while the project has one maintainer; increase this to one after a
  second trusted maintainer exists, because authors cannot approve their own pull requests;
- require linear history and block force pushes and branch deletion;
- apply the rule to administrators.

## Roles

- The repository owner approves scope, releases, visibility, collaborators, and merges.
- CODEOWNERS identifies the default reviewer for every path.
- Contributors own tests, migration safety, compatibility notes, documentation, and
  changelog entries for their changes.
- Automation may open pull requests and report status, but does not merge without owner
  approval.

## Change lifecycle

1. Create or confirm an issue for work that needs product discussion or spans multiple
   sessions.
2. Create a short-lived branch from current `main`.
3. Open a draft pull request early and keep its description and validation current.
4. Complete automated and manual checks, resolve review discussions, and request owner
   approval.
5. Squash-merge, delete the branch, and monitor the next deployment or release.
6. For user-visible regressions, open a follow-up issue and add a regression test.

## Maintenance cadence

- Review Dependabot pull requests weekly and merge only after CI and compatibility review.
- Review open issues, stale pull requests, backup restore coverage, and security reports
  monthly.
- Review supported Go/Node versions, deployment images, dependencies, and retention
  defaults before each minor release.
- Perform a clean installation and previous-version upgrade test for each release.
- Keep only supported release lines documented in `.github/SECURITY.md`.
