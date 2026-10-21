# CLAUDE.md

Context for working on this repo. User-facing docs are in README.md; this file holds what a new session needs and can't easily derive from the code.

## Resuming in a new session (state at the end of 2026-10-21: 7.2.4-1 released, nothing in progress)

1. **Read** this file, then the plan: `~/.claude/plans/if-i-choose-option-spicy-canyon.md`. It has the decisions, the results and the follow-ups. The older `~/.claude/plans/opencloud-custom-build.md` is the first sketch and is superseded.
2. **Check the state:**
   - `git log --oneline -5`, `git status`: the working tree was clean, everything pushed to `origin` (GitHub, **public**: `kovacsakos14/oc-custom`). The release tag is `v7.2.4-1` (signed).
   - `gh run list -L 3`: the release build of `v7.2.4-1` passed and published `ghcr.io/kovacsakos14/oc-custom:7.2.4-1` (digest `sha256:e9b56fe7…`, public, pulls without login).
   - `docker inspect nc2oc-opencloud --format '{{.Config.Image}}'`: the nc2oc test env's OpenCloud was left on `ghcr.io/kovacsakos14/oc-custom:7.2.4-1`. `test/use-custom --official` switches it back.
   - `scripts/fetch` reports whether `upstream/` (gitignored) is still at the pinned tags. Its clones have the patches applied, plus build leftovers (IDP assets, `node_modules`).
3. **What's next is the user's choice; ask which.** Talk it through first. Commit at each logical step with Conventional Commits; push when a step is done. See "Next steps" below.

## Decisions (with the reasons)
- **Base:** OpenCloud's production line, 7.2.x. Rolling releases come every ~3 weeks, often a new major, and are not for customer servers. Don't rebuild for every patch release: rebuild for security fixes or bugs that affect the user, otherwise bundle them.
- **One image:** the official image with only `/usr/bin/opencloud` replaced, and the patched web embedded in the binary (option A). One immutable tag per server means no web/server drift. The rejected option B (web from a mounted folder) risked an old web folder silently winning over a new built-in one.
- **Branding at runtime, not in the build:** OpenCloud merges `themes/_branding/theme.json` over its theme, and the user mounts a folder per server, read-only. In 7.2 the web UI has no branding editor, so branding will be edited from **nc2oc's Manage section** (planned, not started).
- **Move action patched too:** the web's `canBeMoved` required delete permission. Drag and drop and Ctrl+X/V never checked it.
- **The sign-in page patch (IDP)** came later, at the user's request. It's opt-in through `common.signin.*` and looks like upstream without branding.
- **Public repo and public image:** covers the AGPL source offer for the web, and servers pull without tokens.
- **Licences:** server and reva Apache-2.0, web AGPL-3.0, the repo AGPL-3.0. "OpenCloud" is OpenCloud GmbH's trademark, so customers should see the user's branding, not "OpenCloud" passed off as theirs.
- **Commits are GPG-signed:** key `F6FEA88C37C6A221` (ed25519, no passphrase, on this VM and on GitHub, expires 2028-10-20); the repo config sets `commit.gpgsign` and `tag.gpgsign`. The author is `kovacsakos <kovacsakos12@gmail.com>`; the user chose Gmail for this public repo, even though their latest public repo uses the noreply address.
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
- **The nc2oc test env's `.env` quotes `OC_APP_TOKEN`;** strip the quotes.
- **Puppeteer inside the OpenCloud web app:** `page.goto` never settles, because the app keeps a notifications connection open. Navigate in-app (click links), as `test/browser.js` does. The context menu is `#oc-files-context-menu`; actions have `oc-files-actions-<name>-trigger` classes.
- **`IDP_ASSET_PATH`** serves the IDP from disk, layered over the built-in copy. With it, the patched sign-in page can be tested on the official image without a server build. That's how patch 5 was checked.
- **Don't write to the nc2oc fixture as a test user.** An overwrite check replaced `Clients/Dave/readme.txt`; it was restored from its version, then re-uploaded as admin with `X-OC-Mtime` to match Nextcloud's mtime.
- **Known test-env leftovers, not from this repo:**
  - verify.sh's own `new.txt` (gives "admin: files differ" and "dave can't upload 201 vs 204" on a second run);
  - 2 OpenCloud e-mails from 2026-10-20 (`zz-reshare`, `zz-perm`), which give "OpenCloud sent 2 e-mails".

  A reset (`reset.sh`, `up.sh`, `fixture.sh`, `ui-e2e.sh`) clears them.
- **manage-e2e.sh failed once on the custom image** ("Account of acmeteszt" timeout after Disable). It then passed on the official image and on a rerun on the custom image: a timing flake right after a container restart.

## What was verified (7.2.4-1, test env)
- **`test/check-permissions`:**
  - dave (Can upload): items report `NV`, the root `SNVCK`; no `D`/`W`;
  - erin (Can edit) and alice (Can view, Can edit): unchanged.
- **WebDAV as dave:** rename and rename back 201, delete 403.
- **A temporary public "upload" link:** `CKZ`/`Z`, rename 403; the link was deleted.
- **`test/browser`:**
  - dave and erin (User Light) land on "Shared with me", alice (User) on her personal files, all in the table view;
  - dave's context menu has Move to and Rename, no Delete; erin's also has Delete;
  - the branding shows on the sign-in page and in the web UI; without it, the sign-in page looks like upstream's.
- **nc2oc's tests:** `verify.sh` passes everything except the leftovers above; `manage-e2e.sh` passes. `ui-e2e.sh` was not run (it needs a reset).
- **CI:**
  - build ~8 min cold, ~3 min with caches;
  - the smoke test checks the version, the branding and the web UI;
  - `scripts/compat`: all patches apply to v7.2.4 and stable-7.2 (7.2.5); on v8.1.0 only `web/0003` (DriveRedirect) conflicts.

## Next steps
- **The Branding page in nc2oc's Manage section** (the user decided it should live there): see "Follow-up" in the plan. It edits `theme.json`, including `common.signin.*`, and the logos over SSH; SVGs are sanitised.
- **7.2.5:** release PR #3614 is pending upstream. When it's out, decide whether it's worth a rebuild (it's a thumbnails fix plus reindex backports).
- **The November production release** (due 2026-11-17, probably 8.2):
  - rebase `web/0003` (DriveRedirect changed in 8.x);
  - check nc2oc against 8.x;
  - snapshot before upgrading: 7.2 → 8 is one-way.
- **Optionally:**
  - report upstream that the server allows MOVE for "Can upload" but doesn't report `NV`;
  - a clean `ui-e2e.sh` run against the custom image;
  - deploy `7.2.4-1` to a first customer server. Check the compose paths against a real production compose file and remove the user's nginx redirect workaround for User Light (patch 4 replaces it).
