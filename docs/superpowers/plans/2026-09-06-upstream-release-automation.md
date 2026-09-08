# Upstream Release Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Synchronize only upstream published releases, preserve fork customizations on `main`, and automatically build upstream and customized release artifacts.

**Architecture:** A scheduled/manual synchronizer discovers non-draft upstream releases, merges each exact release tag into fork `main`, mirrors the upstream tag, and creates a unique fork tag. Separate tag-dispatchable workflows build binaries, Electron artifacts, and GHCR images from the exact requested ref; ordinary `main` pushes only run validation.

**Tech Stack:** GitHub Actions, Git/GitHub CLI, Go, Bun, Docker Buildx, GHCR.

**Spec:** `docs/superpowers/specs/2026-09-06-upstream-release-automation-design.md`

## Global Constraints

- `main` tracks the latest non-draft upstream GitHub Release, including prereleases; it does not track `upstream/main` between releases.
- The upstream repository is `QuantumNous/new-api`.
- Merge upstream release tags with `git merge --no-ff --no-edit`; never force-push or overwrite an existing tag.
- Fork Docker images publish to `ghcr.io/${{ github.repository }}`; official `calciumion/new-api` publishing must not be used by fork workflows.
- The synchronizer requires repository secret `UPSTREAM_SYNC_TOKEN` with Contents, Actions, and Workflows write permissions because `GITHUB_TOKEN` cannot push tags pointing to workflow-file changes.
- Main-branch validation builds Docker without pushing; formal release workflows publish only from explicit version tags.
- Configuration-only workflow changes do not require production unit-test additions; verify YAML, shell syntax, workflow references, and available backend checks.

---

### Task 1: Persist the release policy for future agents

**Files:**
- Modify: `AGENTS.md`
- Create: `.github/UPSTREAM_RELEASE_WORKFLOW.md`

**Interfaces:**
- Produces the repository policy that future agents must read before changing synchronization or release workflows.

- [ ] **Step 1: Add the concise policy to `AGENTS.md`**

State that `main` is release-driven, list the upstream repository, include RC/beta releases, show the upstream and fork tag formats, and link to `.github/UPSTREAM_RELEASE_WORKFLOW.md`.

- [ ] **Step 2: Add the operational runbook**

Document schedule/manual triggers, conflict handling, GHCR permissions, tag-to-artifact mapping, retry behavior, and the rule that ordinary main pushes validate but do not publish.

- [ ] **Step 3: Verify documentation consistency**

Run `rg -n "release-driven|QuantumNous/new-api|UPSTREAM_RELEASE_WORKFLOW|prerelease|force" AGENTS.md .github/UPSTREAM_RELEASE_WORKFLOW.md` and `git diff --check`. The two documents must agree.

- [ ] **Step 4: Commit**

Run `git add AGENTS.md .github/UPSTREAM_RELEASE_WORKFLOW.md && git commit -m "docs: document release-driven fork automation"`.

### Task 2: Synchronize release tags into `main`

**Files:**
- Create: `.github/workflows/sync-upstream-release.yml`

**Interfaces:**
- Consumes upstream GitHub Releases, fork `main`, and existing fork tags.
- Produces merged `main`, the exact mirrored upstream tag, a deterministic `-fork.<timestamp>.g<sha>` tag, and explicit dispatches for release builders.

- [ ] **Step 1: Define triggers and permissions**

Use a six-hour cron plus `workflow_dispatch`. Grant `contents: write` and `actions: write`; use a concurrency group so two synchronizers cannot mutate `main` at once.

Require `secrets.UPSTREAM_SYNC_TOKEN`, use it for the checkout/`origin` remote and `gh workflow run`, and fail with a clear error when it is absent.

- [ ] **Step 2: Discover unprocessed releases**

Use `gh api --paginate "repos/${UPSTREAM_REPOSITORY}/releases?per_page=100"` and `--jq` to select `.draft == false` and tags beginning with `v`, sort by published/created timestamp, and select only the newest tag. A manual `tag` input may override this with one exact tag; never loop through the entire release history on a scheduled run.

- [ ] **Step 3: Merge without overwriting fork code**

Fetch each exact tag into `refs/tags/upstream/<tag>`, run `git merge --no-ff --no-edit`, and run `git merge --abort` followed by `exit 1` on conflict. Push `HEAD:main` only after a clean merge. Never merge `upstream/main`, reset hard, or force-push.

- [ ] **Step 4: Create tags and dispatch builds**

Push the original upstream tag from its namespaced ref without changing it. Create the fork tag from the updated `main` commit using its UTC timestamp and short SHA. Dispatch `release.yml`, `docker-build.yml`, and `electron-build.yml` with the exact tag through `gh workflow run`.

- [ ] **Step 5: Verify and commit**

Run `git diff --check`, inspect the workflow for no `upstream/main` merge, no force-push, no official image, and explicit tag dispatches, then commit with `git add .github/workflows/sync-upstream-release.yml && git commit -m "ci: sync fork from upstream releases"`.

### Task 3: Validate every updated `main` without publishing

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes pushes to fork `main` and existing backend/frontend jobs.
- Produces backend/frontend test results and a non-publishing amd64 Docker build validation.

- [ ] **Step 1: Add branch/manual triggers**

Keep pull-request validation and add `push: branches: [main]` plus `workflow_dispatch`.

- [ ] **Step 2: Make checkout work for all event types**

Use `${{ github.event.pull_request.base.repo.full_name || github.repository }}` for the repository input so push/manual runs do not receive an empty repository.

- [ ] **Step 3: Add Docker validation**

Add a job requiring `backend` and `frontend`, running only for a main push/manual run. Write a temporary `VERSION=edge-${GITHUB_SHA}` and run a single-platform Buildx build with `push: false`, without registry login.

- [ ] **Step 4: Verify and commit**

Confirm the job has no `push: true` or official image reference, run `git diff --check`, and commit with `git add .github/workflows/ci.yml && git commit -m "ci: validate release-driven main builds"`.

### Task 4: Build binaries and GitHub Releases from explicit tags

**Files:**
- Modify: `.github/workflows/release.yml`

**Interfaces:**
- Consumes `workflow_dispatch.inputs.tag` or a `v*` tag push.
- Produces Linux, macOS, and Windows binaries plus checksums in the GitHub Release for that exact tag.

- [ ] **Step 1: Resolve the requested tag**

Add a required `tag` input, retain tag pushes, checkout the resolved tag with full history, and set `VERSION` from `inputs.tag || github.ref_name`.

- [ ] **Step 2: Include all release classes**

Use a `v*` tag filter with no stable-only exclusions so RC, beta, and `-fork.` tags build. Use the resolved version consistently in filenames and release upload.

- [ ] **Step 3: Verify and commit**

Confirm the workflow targets only the requested tag and contains no official image references, then commit with `git add .github/workflows/release.yml && git commit -m "ci: build binaries for upstream and fork tags"`.

### Task 5: Publish multi-architecture fork images to GHCR

**Files:**
- Modify: `.github/workflows/docker-build.yml`
- Modify: `.github/workflows/docker-image-branch.yml`

**Interfaces:**
- Consumes `workflow_dispatch.inputs.tag` or a `v*` tag push.
- Produces the multi-architecture GHCR image for the exact tag; `latest` is reserved for `-fork.` tags.

- [ ] **Step 1: Resolve the tag and version file**

Use the dispatch input when present and `github.ref_name` for tag pushes. Checkout that tag and write it to `VERSION` before building.

- [ ] **Step 2: Replace official Docker Hub publishing**

Log in to `ghcr.io` with `GITHUB_TOKEN`, derive a lowercase `ghcr.io/${GITHUB_REPOSITORY}` name, publish the exact version tag, and publish `latest` only when the tag contains `-fork.`. Never publish to `calciumion/new-api`.

- [ ] **Step 3: Migrate the manual branch builder too**

Change `.github/workflows/docker-image-branch.yml` to log in to GHCR with `GITHUB_TOKEN`, derive the current repository image name, and use that name for branch/version tags, manifests, signatures, and summaries. Keep its manual branch inputs and multi-architecture behavior.

- [ ] **Step 4: Preserve multi-arch/signing behavior and commit**

Keep the amd64/arm64 matrix, manifest, cache, provenance/SBOM, and cosign steps. Run `git add .github/workflows/docker-build.yml && git commit -m "ci: publish fork release images to ghcr"`.

### Task 6: Make Electron builds follow both tag classes

**Files:**
- Modify: `.github/workflows/electron-build.yml`

**Interfaces:**
- Consumes `workflow_dispatch.inputs.tag` or a `v*` tag push.
- Produces Electron assets for exact upstream and fork release tags.

- [ ] **Step 1: Add tag input and version resolution**

Use the explicit tag for checkout, frontend version, Electron version normalization, artifact names, and release upload.

- [ ] **Step 2: Include RC and fork tags**

Change the filter to `v*` and remove stable-only exclusions so the synchronizer can dispatch both tag classes.

- [ ] **Step 3: Verify and commit**

Confirm release upload uses the resolved tag and run `git diff --check`, then commit with `git add .github/workflows/electron-build.yml && git commit -m "ci: build electron artifacts for fork releases"`.

### Task 7: Full verification and handoff

**Files:**
- Verify all changed workflows, `AGENTS.md`, `.github/UPSTREAM_RELEASE_WORKFLOW.md`, and the design/plan docs.

- [ ] **Step 1: Run repository checks**

Run `git diff --check` and `git status --short`.

- [ ] **Step 2: Validate workflows**

Run `actionlint` if installed. Otherwise parse workflow YAML with an available parser and inspect expressions, triggers, permissions, inputs, and job dependencies.

- [ ] **Step 3: Run available backend/frontend verification**

Create the ignored `web/dist/index.html`, run `GOCACHE="$PWD/.gocache" GOWORK=off go test ./...` from the root and from `relaykit/`, and run `bun run typecheck` plus `bun run test` when Bun is available. Report environment blockers exactly.

- [ ] **Step 4: Review requirements against the spec**

Confirm release tags alone advance `main`; RC/beta tags count; custom commits are merged; conflicts stop before push; exact upstream tags are immutable; fork tags are unique; main validation does not publish; formal builds use GHCR; future agents have the runbook.

- [ ] **Step 5: Commit the complete implementation**

Run `git add AGENTS.md .github docs/superpowers && git commit -m "ci: automate upstream release sync and fork builds"`.
