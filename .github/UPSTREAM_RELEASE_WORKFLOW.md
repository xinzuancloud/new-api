# Upstream Release Workflow

This fork intentionally follows upstream published releases rather than upstream daily commits.

## Repository invariant

`main` contains the latest non-draft release from `QuantumNous/new-api` plus the fork's own committed changes. Non-draft includes prereleases such as `v1.0.0-rc.34` and `v1.0.0-beta.1`; draft releases and ordinary upstream branch commits do not advance `main`.

Do not change this to merge `upstream/main`. The synchronization source is always the exact upstream release tag.

## Automatic flow

`.github/workflows/sync-upstream-release.yml` runs every six hours and can also be started with `workflow_dispatch`.

On a scheduled run, it considers only the newest non-draft upstream release. This prevents the first run from replaying the entire upstream release history. A manual run may provide one exact `tag` for recovery or an intentional historical build. For the selected tag, it:

1. Fetches the exact upstream tag into a namespaced local ref.
2. Merges that tag into fork `main` with `git merge --no-ff --no-edit`, keeping fork commits.
3. Stops and aborts the merge if there is a conflict. It does not push a partial branch or publish a partial release.
4. Pushes the updated `main` and mirrors the exact upstream tag without changing it.
5. Creates a fork tag from the updated `main` commit.
6. Explicitly dispatches the binary, Docker, and Electron release workflows for both tags.

The explicit dispatch is intentional: a push made with the repository `GITHUB_TOKEN` does not recursively start ordinary `push` workflows.

## Version mapping

For upstream tag `v1.0.0-rc.34`:

| Build | Tag | Source |
| --- | --- | --- |
| Upstream | `v1.0.0-rc.34` | Exact upstream tag |
| Fork | `v1.0.0-rc.34-fork.20260906.t143000.gabc1234` | Fork `main` after the merge |

The fork suffix includes the source commit timestamp and short SHA. A retry for the same commit reuses the same fork tag; a different commit gets a different tag even on the same day.

## What happens on ordinary changes

Pushing additional custom commits to `main` does not create a formal release. `ci.yml` runs backend/frontend validation and a Docker build with `push: false`. This catches build regressions without filling the Releases page with one release per commit.

If an interim custom release is needed before the next upstream release, use the manual tag input in the release workflows and use the same `-fork.<timestamp>.g<sha>` naming convention.

## Artifacts

- GitHub Releases contain the platform binaries, checksums, and Electron assets for the requested tag.
- Docker images publish to the current fork's lowercase GHCR path, such as `ghcr.io/xinzuancloud/new-api:<tag>`.
- The exact upstream tag publishes only its versioned image.
- A `-fork.` tag publishes its versioned image and updates `latest`.
- Main validation never logs in to a registry and never pushes an image.

## Required repository settings

The repository's Actions settings must allow workflows to write repository contents, dispatch workflows, create releases, and write packages. Configure a repository secret named `UPSTREAM_SYNC_TOKEN` for the synchronizer. It must be a token authorized for this repository with Contents, Actions, and Workflows write permissions; `GITHUB_TOKEN` alone cannot push a tag that points to a commit changing workflow files. GHCR authentication uses the workflow's `GITHUB_TOKEN`; no Docker Hub credentials are needed for fork releases.

## Safe change rules for agents

- Do not use `git reset --hard` against the remote branch or any force-push.
- Do not overwrite an existing upstream or fork tag.
- Do not point fork workflows at the official `calciumion/new-api` image.
- Keep RC and beta tags included in `v*` release filters.
- If a workflow's behavior changes, update this file and the corresponding section in `AGENTS.md` in the same change.
