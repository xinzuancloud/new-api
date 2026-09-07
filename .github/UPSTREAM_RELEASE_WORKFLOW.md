# Upstream Release Workflow

This fork intentionally follows upstream published releases rather than upstream daily commits.

## Repository invariant

`main` contains the latest non-draft release from `QuantumNous/new-api` plus the fork's own committed changes. Non-draft includes prereleases such as `v1.0.0-rc.34` and `v1.0.0-beta.1`; draft releases and ordinary upstream branch commits do not advance `main`.

Do not change this to merge `upstream/main`. The synchronization source is always the exact upstream release tag.

## Automatic flow

`.github/workflows/sync-upstream-release.yml` runs every six hours and can also be started with `workflow_dispatch`.

On a scheduled run, it considers only the newest non-draft upstream release. This prevents the first run from replaying the entire upstream release history. A manual run may provide one exact `tag` for recovery or an intentional historical build. For the selected tag, it:

1. Disables and verifies the legacy publisher workflow IDs (`release.yml`, `docker-build.yml`, `electron-build.yml`), then fetches the exact upstream tag into a namespaced local ref.
2. Merges that tag into fork `main` with `git merge --no-ff --no-edit`, keeping fork commits.
3. Stops and aborts the merge if there is a conflict. It does not push a partial branch or publish a partial release.
4. Pushes the updated `main` and mirrors the exact upstream tag without changing it.
5. Creates a fork tag from the updated `main` commit.
6. Explicitly dispatches `fork-release.yml`, `fork-docker-build.yml`, and `fork-electron-build.yml` from `main`, once for each tag.

The dedicated publishers accept only `workflow_dispatch` and check out the explicit tag. `UPSTREAM_SYNC_TOKEN` pushes do trigger tag workflows, so the legacy workflow IDs must remain disabled at repository level: immutable upstream tags still contain their original publishing definitions, including Docker Hub targets and prerelease exclusions. Never re-enable those IDs for a retry. This prevents duplicate uploads while keeping the exact upstream tag unchanged. The new run titles include the target tag; GitHub may still show `main` as the workflow source.

A scheduled run skips an already synchronized pair of tags. An explicit manual tag reuses the existing refs and dispatches publication again, without rewriting tags. Each dedicated publisher serializes retries for the same tag with `cancel-in-progress: false`. Main pushes from the PAT already trigger `ci.yml`; the synchronizer does not dispatch a duplicate CI run.

## Version mapping

For upstream tag `v1.0.0-rc.34`:

| Build | Tag | Source |
| --- | --- | --- |
| Upstream | `v1.0.0-rc.34` | Exact upstream tag |
| Fork | `v1.0.0-rc.34-fork.20260906.t143000.gabc1234` | Fork `main` after the merge |

The fork suffix includes the source commit timestamp and short SHA. A retry for the same commit reuses the same fork tag; a different commit gets a different tag even on the same day.

## What happens on ordinary changes

Pushing additional custom commits to `main` does not create a formal release. `ci.yml` runs backend/frontend validation and a Docker build with `push: false`. This catches build regressions without filling the Releases page with one release per commit.

If an interim custom release is needed before the next upstream release, use the manual tag input in the dedicated `fork-*` release workflows and use the same `-fork.<timestamp>.g<sha>` naming convention.

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

## Recovery of the rc.34 publication failure

The architecture images were pushed successfully, but the manifest job referenced a malformed cosign-installer commit. The correct v4.1.2 pin is `6f9f17788090df1f26f669e9d70d6ae9567deba6`. Use the dedicated Docker workflow with the existing tag to rebuild/reconcile its manifests and signatures. Do not move or overwrite the Git tag. Legacy upstream Docker Hub jobs and duplicate asset uploads are avoided by keeping the old publishing workflows disabled.

Validation: `python3 -m unittest discover -s .github/tests -v` exercises the synchronizer with isolated command fixtures, including fail-closed legacy-workflow checks, fresh tags, manual recovery, and scheduled no-op behavior. Run `actionlint` on the changed workflow files before publishing.

Dedicated binary publishers resolve the checked-out Go module with `go list -m` and inject the explicit requested tag into `<module>/common.Version`. Native binaries are checked with `VERSION` removed from the environment, so an environment override cannot hide an incorrect linker target. Electron uses the explicit tag as well, rather than choosing another tag at the same commit with `git describe`.
