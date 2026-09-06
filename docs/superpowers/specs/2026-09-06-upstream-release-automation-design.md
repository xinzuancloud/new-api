# Upstream Release Automation Design

**Status:** Approved for implementation

## Goal

Keep the fork's `main` branch at the latest published upstream release, while preserving the fork's custom commits. For every new upstream release, including release candidates, build both the exact upstream source and the merged fork source as independently identifiable releases.

## Repository and release model

- Upstream source: `QuantumNous/new-api`.
- The fork's `main` branch does not follow `upstream/main` between releases.
- A published upstream release means every non-draft GitHub Release, including prereleases such as `-rc` and `-beta` tags.
- Draft releases and non-release branch commits are ignored by the synchronizer.
- `main` is advanced by merging the exact upstream release tag into the current fork `main`; existing fork commits are preserved.
- A merge conflict stops the synchronization before the fork's `main` is pushed and before either release is published.

## Version and artifact model

For an upstream tag such as `v1.0.0-rc.34`, the automation creates:

| Artifact | Tag/version | Source |
| --- | --- | --- |
| Upstream release | `v1.0.0-rc.34` | Exact upstream release tag |
| Fork release | `v1.0.0-rc.34-fork.YYYYMMDD.tHHMMSS.g<sha>` | Fork `main` after merging that upstream tag |

The upstream tag remains exact so the fork can reproduce the original release. The fork suffix includes the source commit timestamp and short commit SHA, making repeated fork builds from different commits unique while making retries for the same commit idempotent.

For main-branch pushes, CI runs backend/frontend checks and a non-publishing Docker build validation. It does not create a GitHub Release or push a stable image. A manual custom-release dispatch may publish a later fork build based on the same upstream version when needed.

## Workflow data flow

1. `sync-upstream-release.yml` runs on a schedule and through `workflow_dispatch`.
2. It lists upstream non-draft releases, sorts their tags oldest-to-newest, and skips tags already present in the fork.
3. For each unprocessed tag, it fetches the exact upstream tag into a namespaced local ref, merges it into fork `main` with `--no-ff`, and aborts on conflicts.
4. It pushes the updated `main`, mirrors the exact upstream tag, creates the deterministic fork tag, and dispatches the release workflows explicitly. Explicit dispatch is required because pushes made with `GITHUB_TOKEN` do not recursively trigger ordinary `push` workflows.
5. `ci.yml` validates pushes to `main`; the Docker validation job builds without pushing.
6. `release.yml`, `docker-build.yml`, and `electron-build.yml` accept a tag input and build from the exact requested tag. They publish to the fork's GitHub Release and GHCR namespace, never to the upstream Docker Hub image.

## Failure and retry behavior

- If upstream metadata cannot be read, the sync run fails without changing the fork.
- If a merge conflicts, the run aborts the merge and leaves the remote `main` unchanged.
- If a tag or release already exists, the relevant step is skipped or treated as idempotent; it never force-updates an existing tag.
- If a release build fails after `main` and tags have been pushed, rerunning the corresponding workflow with the same tag retries the build without changing source history.
- The workflow uses repository `GITHUB_TOKEN` permissions for contents, actions dispatch, packages, and releases. Repository Actions settings must allow workflows to write contents and packages.

## Persistent agent guidance

The root `AGENTS.md` contains a concise operational summary and links to `.github/UPSTREAM_RELEASE_WORKFLOW.md`. Agents must treat the release-driven `main` policy as a repository invariant: do not change the synchronizer back to tracking `upstream/main`, do not force-update mirrored tags, and do not point fork builds at the official image.
