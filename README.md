# oc-custom

[OpenCloud](https://github.com/opencloud-eu/opencloud) with a few small patches, built as a drop-in replacement for the official image. This is an unofficial build, not made or supported by OpenCloud GmbH.

Based on OpenCloud **7.2.4** (reva v2.46.8, web v7.1.5); see `versions.env`. Image: `ghcr.io/kovacsakos14/oc-custom:7.2.4-<build>`.

## Contents
- [What's changed](#whats-changed)
- [Deploying](#deploying)
- [Branding](#branding)
- [Building](#building)
- [Updating to a new OpenCloud release](#updating-to-a-new-opencloud-release)
- [Rolling back](#rolling-back)
- [Testing](#testing)
- [Status](#status)
- [Licence](#licence)

## What's changed
| # | Change | Patch |
|---|---|---|
| 1 | **"Can upload" shares can rename and move items.** The server already accepts the move, but didn't report it to the apps (`oc:permissions` had no `NV`), so the web UI and the desktop app didn't offer it. Delete and editing stay off (no `D`, no `W`). Public links are unchanged. | `patches/reva/0001-…` |
| 2 | **The web UI's Move action needs only rename permission,** no longer delete permission too, so "Can upload" users get it. Drag and drop and cut/paste had no such check. Moving to another space is a copy, as before. | `patches/web/0001-…` |
| 3 | **The table is the default file view** instead of tiles. Users who already picked a view keep it (it's stored in their browser). | `patches/web/0002-…` |
| 4 | **Accounts without a personal space (User Light) land on "Shared with me"** instead of the empty Spaces list. Others are unchanged. | `patches/web/0003-…` |
| 5 | **The sign-in page follows the branding:** its tab title, tab icon, colours, footer and, from 7.2.4-2, a background image (see [Branding](#branding)). Without branding it looks exactly as upstream's. | `patches/server/0001-…` |

Everything else is upstream's: the image is the official one with only `/usr/bin/opencloud` replaced, built from the same sources and with the same toolchain and target. `opencloud version` and `status.php` report `7.2.4+custom.<build>`.

## Deploying
Compared with a compose file for the official image, two lines change:
```yaml
services:
  opencloud:
    image: ghcr.io/kovacsakos14/oc-custom:7.2.4-2   # was: opencloudeu/opencloud:7.2.4
    volumes:
      - ./config:/etc/opencloud
      - ./data:/var/lib/opencloud
      # optional: this server's branding (see below)
      - ./branding:/var/lib/opencloud/web/assets/themes/_branding:ro
```
- **Update or roll back** by changing the tag, then `docker compose pull && docker compose up -d`.
- **Tags never change** once published: `7.2.4-1`, `7.2.4-2`, … each mean one build. There's no `latest`, so a server only changes when you change its tag. For extra certainty, pin the digest as well (`…:7.2.4-1@sha256:…`).
- **The image is public,** so servers pull it without logging in. It holds nothing secret: config and branding stay on each server.

## Branding
OpenCloud merges `themes/_branding/theme.json` over its own theme on every request. So branding is a folder per server, with no build and no restart: change a file and reload the browser.

`branding/example/` is a complete example. A minimal `theme.json` sets only what it changes:
```json
{
  "common": {
    "name": "Acme Files",
    "slogan": "Acme Files – file sharing for Acme",
    "logo": "themes/_branding/logo.svg",
    "urls": { "imprint": "https://example.com/imprint", "privacy": "https://example.com/privacy" },
    "signin": {
      "background": "#431407", "accent": "#fb923c", "onAccent": "#431407",
      "footer": "Acme Files – file sharing for Acme",
      "backgroundImage": "themes/_branding/background.jpg"
    }
  },
  "clients": { "web": { "defaults": {
    "logo": "themes/_branding/logo-white.svg",
    "logoMobile": "themes/_branding/logo-mobile.svg",
    "favicon": "themes/_branding/favicon.svg"
  } } }
}
```
| Key | Where it shows |
|---|---|
| `common.logo` | The sign-in page (large, on its background) |
| `common.name` | The browser tab titles ("Sign in - Acme Files", "Personal - Acme Files") |
| `common.signin.background` | The sign-in page's background colour |
| `common.signin.accent` | The sign-in page's button, input focus ring and hexagon |
| `common.signin.onAccent` | The text on the sign-in button |
| `common.signin.footer` | The sign-in page's footer text (`""` hides it) |
| `common.signin.backgroundImage` | A picture behind the sign-in page, in place of the hexagon (from 7.2.4-2) |
| `common.urls.imprint`, `.privacy` | Links in the web UI |
| `clients.web.defaults.logo` | The top bar of the web UI (dark by default, so use a light logo) |
| `clients.web.defaults.logoMobile` | The top bar on narrow screens |
| `clients.web.defaults.favicon` | The browser tab icon, on the sign-in page and in the web UI |

- **Logos** are SVG or PNG files in the same folder, referenced as `themes/_branding/<file>`.
- **Colours:** `clients.web.themes` holds the light and dark theme. It's a list, and a list replaces upstream's as a whole, so copy both entries, as `theme.colors.example.json` does, and change the `primary`/`chrome` tokens. A theme entry can also set its own `logo` (the example uses the white logo in dark mode).
- **The sign-in page's background image** (`common.signin.backgroundImage`): a file in the folder, referenced like the logos, or an `https://` URL. It covers the page (scaled to fill, centred) and replaces the hexagon; `common.signin.background` still shows while it loads, so pick a matching colour. Use a JPEG or WebP of about 1920×1080 and a few hundred KB. Remove the key to get the plain background back. Upstream's environment setting `IDP_LOGIN_BACKGROUND_URL` still works and wins over the key.
- **The sign-in page keys are ours** (patch 5): upstream's sign-in page ignores the theme apart from the logo. Colours accept CSS colour values (`#rrggbb`, `rgb(…)`, names). Keys you leave out keep upstream's look.
- **Read-only mount:** the folder is the only source of truth. OpenCloud's own logo upload API (`/branding/logo`, which the 7.2 web UI doesn't use) can't change it. Edit it on the host (for example over SSH, by hand or with a management tool); the container only reads it.
- **Source link (AGPL):** link this repository from the imprint page, or add another entry under `common.urls`.

## Building
The build needs more memory than a small VM has (the Go link step alone takes several GB), so it runs in **GitHub Actions** (`.github/workflows/build.yml`):
- **A tag `v7.2.4-<build>`** builds, smoke-tests and publishes `:7.2.4-<build>`. The tag must match `OC_VERSION` and `BUILD` in `versions.env`, and an existing image tag is never overwritten.
- **A manual run** (Actions → Build → Run workflow) publishes `:test-<commit>` for trying a build before tagging it.

`scripts/build-native` is what CI runs, and it works on any machine with Docker and about 8 GB of RAM:
1. Fetch the upstream sources at the pinned versions and apply the patches.
2. Build the patched web, cached in `out/web-dist` and rebuilt only when its version or patches change.
3. Build the IDP frontend (patched), then the server binary in upstream's `golang-ci` image with the official image's target (`release-linux-docker-amd64`, libvips). The server vendors its dependencies, so the reva patches go into its vendored reva (the same version as `REVA_VERSION`).
4. Copy the binary into the official image (`Dockerfile`), and check that it runs and finds its libraries.

Caches (pnpm store, Go modules and build cache) are kept between CI runs. Only amd64 is built.

## Updating to a new OpenCloud release
**Which releases to follow.** OpenCloud has three kinds (see their [release lifecycle](https://docs.opencloud.eu/docs/admin/resources/lifecycle)):
- **Rolling,** about every 3 weeks, often a new major.
- **Production,** about every 6 months, with patch releases on a `stable-X.Y` branch.
- **LTS,** for subscribers only.

This build follows **production**: 7.2.x now. Don't rebuild for every patch release: rebuild when it fixes something that matters to you (security, a bug you hit), and bundle the rest.

**Compatibility watch.** `.github/workflows/compat.yml` runs weekly, and you can run `scripts/compat` locally. It checks, without building, whether the patches still apply to:
- the latest patch release of our line;
- its stable branch (what the next patch release will be);
- as a heads-up only, the latest release.

If a patch stops applying to our line, it opens an issue.

**Steps:**
1. Check that the compat workflow is green for the new version.
2. In `versions.env`, set `OC_VERSION` to the new tag. Set `REVA_VERSION` and `WEB_VERSION` to what that tag pins: the reva line in its `go.mod`, and `WEB_ASSETS_VERSION` in `services/web/Makefile`; `scripts/compat <tag>` prints both. Update `BASE_IMAGE` (with its digest: `docker buildx imagetools inspect opencloudeu/opencloud:<version>`) and reset `BUILD=1`.
3. Run `scripts/fetch --force && scripts/apply`. If a patch conflicts, fix it in `upstream/` and run `scripts/refresh-patches`.
4. Push, run the Build workflow manually, and test the `:test-…` image (see [Testing](#testing)).
5. Tag `v<version>-1`, then deploy.

Branding folders carry over; check the release notes for changes to the theme.

**For a fix to the patches on the same OpenCloud version,** raise `BUILD` and tag `v7.2.4-<BUILD>`.

**Upstream's unit tests:** the table patch doesn't update upstream's snapshot tests that record the default view (AppBar, Projects, trash Overview, admin Users and Spaces), so those would fail in upstream's test suite. The build doesn't run them.

## Rolling back
OpenCloud doesn't refuse to run an older version, but upgrades can be one-way: some storage migrations have no rollback, and config can change between majors.
- **Our own rebuilds** (`7.2.4-1` → `7.2.4-2`) only change the patches, not stored data: switch the tag back.
- **Patch releases** (`7.2.4` → `7.2.5`) normally don't change data formats; read the release notes.
- **Minor or major upgrades** (7.2 → 8.x) are one-way. Before upgrading, stop the container and snapshot or copy `/var/lib/opencloud` and `/etc/opencloud`. To roll back, restore both together with the old tag. Anything users changed since the upgrade is lost by the restore.

## Testing
Builds are tested in a separate OpenCloud test environment (Docker Compose, not part of this repo) with test users of each role and folder shares of each kind. The helpers in `test/` work with any such environment: copy `test/local.env.example` to `test/local.env` (gitignored) and set its compose command, URL and admin app token.
- `test/use-custom <image> [branding dir]` runs the test environment's OpenCloud on another image, with a branding folder (default `branding/example`); `test/use-custom --official` runs upstream's image of the same version without branding.
- `test/browser <out dir> user:password …` signs in as each user in a fresh browser profile. It saves screenshots and records where they landed and whether files show as a table. With `ACTIONS="client1:Client folder/readme.txt"` it also opens that file's context menu and lists its actions (rename, move, delete…).
- `test/check-permissions <user> <share>` lists what OpenCloud reports for each item in a share (`oc:permissions`) as the recipient sees it. A "Can upload" share shows `NV`, with no `D` or `W`.
- `scripts/smoke-test <image>` starts an image with a fresh config and checks the version, the branding and the web UI (CI runs it on every build).

**What to check for each new build:**
- **A "Can upload" recipient:** items report `NV` and no `D`/`W`. Rename, the Move action, drag and drop and cut/paste work; delete is neither offered nor allowed.
- **A public upload link:** no rename/move.
- **Can view and Can edit shares:** unchanged.
- **A fresh browser:** files show as a table. A User Light account lands on "Shared with me"; a User lands on their personal files.
- **The branding:** it shows on the sign-in page and in the web UI. Without a branding folder, the sign-in page looks like upstream's.
- **The test environment's own end-to-end tests still pass** (migration, user and share management).

## Layout
- `versions.env`: the upstream versions, the base image and our build number.
- `patches/server`, `patches/reva`, `patches/web`: the changes, as plain diffs. No two patches touch the same file.
- `scripts/fetch`: clone upstream at those versions into `upstream/` (gitignored).
- `scripts/apply`: apply the patches.
- `scripts/refresh-patches`: rewrite the patches from `upstream/`.
- `scripts/compat`: check that the patches apply to other versions.
- `scripts/build-native`, `Dockerfile`: the build.
- `scripts/smoke-test`: the image check.
- `branding/example`: an example branding folder.
- `test/`: the test helpers (`local.env.example` for your test environment).
- `CLAUDE.md`: working notes for continuing development (decisions, findings, next steps).

## Status
**Released:** `7.2.4-2` (2026-10-21): adds the sign-in background image (`common.signin.backgroundImage`) to patch 5; otherwise the same as `7.2.4-1`.

`7.2.4-1` (2026-10-21), OpenCloud 7.2.4 with patches 1–5. Tested:
- what a "Can upload" recipient is told and can do (rename and move yes, delete no);
- public upload links unchanged;
- landing pages and the table view in a fresh browser;
- the context menu actions;
- the branding on the sign-in page and in the web UI;
- the test environment's end-to-end tests (migration, user and share management).

**Upstream compatibility** (`scripts/compat`, 2026-10-21):

| OpenCloud | Patches |
|---|---|
| 7.2.4, stable-7.2 (the coming 7.2.5) | All apply |
| 8.1.0 (rolling) | All apply except `web/0003` (User Light → "Shared with me"); its code changed in 8.x and needs a rebase |

**Next:**
- **7.2.5:** decide whether it's worth a rebuild once it's released.
- **The next production release** (due mid-November 2026, probably 8.x): rebase `web/0003`, test it, and snapshot servers before upgrading, since 7.2 → 8 is one-way.

## Licence
- **Upstream:** OpenCloud's server and reva are Apache-2.0; OpenCloud Web is AGPL-3.0.
- **This repository** (the patches and scripts) is AGPL-3.0; see `LICENSE`.
- **Corresponding source:** anyone using an instance running this build can get its source here. That's upstream at the versions in `versions.env` (each image tag `X-N` matches the git tag `vX-N`) plus `patches/`.
- **Trademark:** "OpenCloud" is a trademark of OpenCloud GmbH. This build is not affiliated with them.
