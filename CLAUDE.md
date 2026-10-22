# CLAUDE.md

Context for working on this repo. User-facing docs are in README.md; this file holds what a new session needs and can't easily derive from the code. **This repo is public:** notes about the private test environment, the tools around it and the plan files are in `CLAUDE.local.md` (gitignored); read it too when it exists, and keep such details out of committed files.

## Resuming in a new session (state on 2026-10-22: 7.2.4-3 released; optional Authelia (2FA) decided and prototyped, not built here yet)

1. **Read** this file and `CLAUDE.local.md` (the plan file and the test environment are named there).
2. **Check the state:**
   - `git log --oneline -5`, `git status`: the working tree was clean, everything pushed to `origin` (GitHub, **public**: `kovacsakos14/oc-custom`). The latest release tag is `v7.2.4-3` (signed; `v7.2.4-2` and `v7.2.4-1` before it).
   - `gh run list -L 3`: the release build of `v7.2.4-3` passed (2026-10-21) and published `ghcr.io/kovacsakos14/oc-custom:7.2.4-3` (public, pulls without login). Earlier: `7.2.4-2` (`sha256:10f039a2…`), `7.2.4-1` (`sha256:e9b56fe7…`).
   - The test environment (`test/local.env`, see `CLAUDE.local.md`): `test/use-custom <image>` swaps its OpenCloud's image, `--official` runs upstream's.
   - `scripts/fetch` reports whether `upstream/` (gitignored) is still at the pinned tags. Its clones have the patches applied, plus build leftovers (IDP assets, `node_modules`).
3. **What's next is the user's choice; ask which.** Talk it through first. Commit at each logical step with Conventional Commits; push when a step is done. See "Next steps" below.

## Decisions (with the reasons)
- **Base:** OpenCloud's production line, 7.2.x. Rolling releases come every ~3 weeks, often a new major, and are not for customer servers. Don't rebuild for every patch release: rebuild for security fixes or bugs that affect the user, otherwise bundle them.
- **One image:** the official image with only `/usr/bin/opencloud` replaced, and the patched web embedded in the binary (option A). One immutable tag per server means no web/server drift. The rejected option B (web from a mounted folder) risked an old web folder silently winning over a new built-in one.
- **Branding at runtime, not in the build:** OpenCloud merges `themes/_branding/theme.json` over its theme, and the user mounts a folder per server, read-only. In 7.2 the web UI has no branding editor; the folder is edited on the host, by a separate management tool (planned outside this repo). This repo owns the folder's format: if a change adds or renames `theme.json` keys, update the README "Branding".
- **Move action patched too:** the web's `canBeMoved` required delete permission. Drag and drop and Ctrl+X/V never checked it.
- **The sign-in page patch (IDP)** came later, at the user's request. It's opt-in through `common.signin.*` and looks like upstream without branding.
- **Public repo and public image:** covers the AGPL source offer for the web, and servers pull without tokens.
- **Licences:** server and reva Apache-2.0, web AGPL-3.0, the repo AGPL-3.0. "OpenCloud" is OpenCloud GmbH's trademark, so customers should see the user's branding, not "OpenCloud" passed off as theirs.
- **Commits are GPG-signed:** key `F6FEA88C37C6A221` (ed25519, no passphrase, on this VM and on GitHub, expires 2028-10-20); the repo config sets `commit.gpgsign` and `tag.gpgsign`. The author is `kovacsakos <kovacsakos12@gmail.com>`.
- **Optional sign-in with 2FA: Authelia, a tested companion, not part of the image** (decided 2026-10-22; README "Status"). OpenCloud's built-in IdP has no 2FA. Authelia runs in its own container in the same compose stack under `/authelia` on OpenCloud's own name; OpenCloud with `OC_EXCLUDE_RUN_SERVICES=idp`, `PROXY_AUTOPROVISION_ACCOUNTS=false`, `PROXY_ROLE_ASSIGNMENT_DRIVER=default` (the `oidc` driver breaks the desktop client: opencloud-compose#111, desktop#217); users stay in OpenCloud's IDM, Authelia's file backend holds passwords, groups and TOTP devices; 2FA only for an admins group. Not in the image because of Authelia's own release cycle, data and process (no supervisor in this image). Instead: `versions.env` pins the tested Authelia version, the repo holds the reference setup and the tests cover the pair. **Without Authelia nothing changes:** the built-in IdP and upstream's password dialog. Patch 6 (web, planned) makes "Change password" open a URL from the web config, and is a no-op without it.
- **Builds run in GitHub Actions only:** this VM (2 vCPU, 3 GB RAM, ~1 GB free) can't link the server. Small parts can be built locally: the IDP frontend took 2 min, the web's `pnpm install` 75 s.

## Findings (things that cost time)
- **The server repo vendors its dependencies:** a `go.mod` replace of reva fails `go-generate` with "inconsistent vendoring". So `scripts/build-native` applies `patches/reva` to `vendor/github.com/opencloud-eu/reva/v2` (identical to the pinned reva tag, without tests). The `upstream/reva` clone is only for reva's unit tests and for `scripts/compat`.
- **The official binary isn't static:** it's built with `release-linux-docker-amd64` and links Alpine's libvips. `golang-ci:1.25` and the 7.2.4 image are both Alpine 3.24.1 with vips 8.18.2, so the binary drops in; `build-native` checks `ldd`. Re-check this when the base changes.
- **The version string:** `VERSION=v7.2.4 STRING=custom.<BUILD> EDITION=stable` gives `7.2.4+custom.1` in `opencloud version`, in `status.php` and in the web UI's footer.
- **Upstream's toolchain:**
  - The IDP needs `quay.io/opencloudeu/nodejs-ci:24` (pnpm 10), with the **whole server repo mounted**, because its Makefile includes `../../.make`.
  - The web pins pnpm 11.5.2, so it builds in `node:24-alpine` with corepack.
- **Vitest 4's `-u`/`--update` takes an optional value:** `pnpm test:unit --run -u <file>` swallows the file and runs the whole suite (an hour on this VM). Put the flag last: `pnpm test:unit --run <file> -u`.
- **The table patch doesn't update upstream's snapshots,** which record the default view (AppBar, Projects, trash Overview, admin Users and Spaces), because they'd conflict on every release. Those snapshot tests expect tiles. The build doesn't run them.
- **Patch robustness:**
  - The reva test lives in its own file (`role_custom_test.go`): appending to `role_test.go` conflicted with 8.x.
  - No two patches may touch the same file (`scripts/refresh-patches` rewrites each patch from the files it already touches).
- **`curl … | grep -q` under `set -o pipefail` fails on large responses:** grep exits early and curl's broken pipe fails the pipeline. Read responses into a variable first (`scripts/smoke-test`).
- **Puppeteer inside the OpenCloud web app:** `page.goto` never settles, because the app keeps a notifications connection open. Navigate in-app (click links), as `test/browser.js` does. The context menu is `#oc-files-context-menu`; actions have `oc-files-actions-<name>-trigger` classes.
- **`IDP_ASSET_PATH`** serves the IDP from disk, layered over the built-in copy. With it, the patched sign-in page can be tested on the official image without a server build. That's how patch 5 was checked.
- **Don't write to the test environment's test data as a test user;** checks that change files belong to its own tests. See `CLAUDE.local.md`.

## What was verified (7.2.4-1, test env)
- **`test/check-permissions`:**
  - a Can upload recipient: items report `NV`, the root `SNVCK`; no `D`/`W`;
  - Can edit and Can view recipients: unchanged.
- **WebDAV as the Can upload recipient:** rename and rename back 201, delete 403.
- **A temporary public "upload" link:** `CKZ`/`Z`, rename 403; the link was deleted.
- **`test/browser`:**
  - User Light accounts land on "Shared with me", a User on their personal files, all in the table view;
  - the Can upload recipient's context menu has Move to and Rename, no Delete; a Can edit recipient's also has Delete;
  - the branding shows on the sign-in page and in the web UI; without it, the sign-in page looks like upstream's.
- **The test environment's end-to-end tests** pass on it (details in `CLAUDE.local.md`).
- **7.2.4-2 (sign-in background image):** tested through `IDP_ASSET_PATH` and then on the released image with `test/browser`. The theme's image shows and the hexagon goes; `IDP_LOGIN_BACKGROUND_URL` still wins; without branding the page is upstream's. The rest of 7.2.4-2 is 7.2.4-1 unchanged (only `patches/server/0001` changed).
- **CI:**
  - build ~8 min cold, ~3 min with caches (manual runs only: since 2026-10-21 releases build without caches, and the node image and pnpm are pinned by digest/hash; untested in CI until the next build);
  - the smoke test checks the version, the branding (including the background image) and the web UI;
  - `scripts/compat`: all patches apply to v7.2.4 and stable-7.2 (7.2.5); on v8.1.0 only `web/0003` (DriveRedirect) conflicts.

## Next steps
- **Authelia companion (agreed 2026-10-22; details and the prototype's findings in `CLAUDE.local.md`):** patch 6 (`accountPreferences.vue`: "Change password" → URL from the web config, upstream dialog without it; keep it in its own file set, like the other patches); `AUTHELIA_VERSION` (+ digest) in `versions.env`; the reference setup (compose, Authelia config template with the four clients `web`, `OpenCloudDesktop`, `OpenCloudAndroid`, `OpenCloudIOS`, the admins `two_factor` rule, `/authelia` path, CSP, nginx/SWAG snippet); tests for the pair (web sign-in, admin TOTP, desktop PKCE flow, the password link) and a check with the real SWAG image; README "Deploying" with both variants. Talk the order through with the user first.
- **The branding editor** is built outside this repo (see `CLAUDE.local.md`). This repo only owns the folder's format: if a change here adds or renames `theme.json` keys, update the README "Branding" and say so in `CLAUDE.local.md`.
- **7.2.5:** release PR #3614 is pending upstream. When it's out, decide whether it's worth a rebuild (it's a thumbnails fix plus reindex backports).
- **The November production release** (due 2026-11-17, probably 8.2):
  - rebase `web/0003` (DriveRedirect changed in 8.x);
  - check the tools that manage these servers against 8.x;
  - snapshot before upgrading: 7.2 → 8 is one-way.
- **Optionally:**
  - report upstream that the server allows MOVE for "Can upload" but doesn't report `NV`;
  - deploy `7.2.4-2` to a first customer server. Check the compose paths against a real production compose file and remove any nginx redirect workaround for User Light (patch 4 replaces it).
