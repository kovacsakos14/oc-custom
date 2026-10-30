# oc-custom

[OpenCloud](https://github.com/opencloud-eu/opencloud) with a few small patches, built as a drop-in replacement for the official image. This is an unofficial build, not made or supported by OpenCloud GmbH.

Based on OpenCloud **7.2.4** (reva v2.46.8, web v7.1.5); see `versions.env`. Image: `ghcr.io/kovacsakos14/oc-custom:7.2.4-<build>`.

The aim is a plain file server: shared folders first, nothing on screen that isn't used. From 7.2.4-6 the file-server changes apply to **two user types of our own, Client and Employee**, and to **a share role of our own, "Can't delete"**. Upstream's types (Admin, Space Admin, User, User Light) and share roles behave as upstream ships them.

## Contents
- [User types and the "Can't delete" role](#user-types-and-the-cant-delete-role)
- [What's changed](#whats-changed)
- [Deploying](#deploying)
- [Branding](#branding)
- [Building](#building)
- [Updating to a new OpenCloud release](#updating-to-a-new-opencloud-release)
- [Rolling back](#rolling-back)
- [Testing](#testing)
- [Status](#status)
- [Licence](#licence)

## User types and the "Can't delete" role
Built into the image (from 7.2.4-6): no settings file and no environment variable, and a server's compose file stays as for the official image.

| | Client | Employee |
|---|---|---|
| Personal space, Favorites, public links | no | no |
| Shares their own folders | no | no |
| Folders shared to them as | "Can't delete" | upstream's "Can edit" |
| Preferences they may change | language | language, e-mail notifications (per event, interval) |
| Web | every change in the [table](#whats-changed) marked Client | the same, but text files can be created |

- **Assigning:** Admin settings → Users → Role (OpenCloud calls the types "roles"), `deploy/users.sh add|role … client|employee`, or Graph (`POST /graph/v1.0/users/{id}/appRoleAssignments` with the ids below). Admins stay upstream's Admin and see upstream's web UI, apart from the changes marked "everyone".
- **"Can't delete"** is offered in the share dialog for folders, between "Can upload" and "Can edit": view, download, upload, new folders, rename and move; no editing and no deleting. Its files open read-only in the apps (the Text Editor has no Save), and the desktop app marks them read-only and turns a local edit into a conflict copy. The server refuses an upload over an existing file that has content (from any client: web, desktop and phone apps, WebDAV), so nothing on such a share can be lost; an empty file may be replaced, because Windows Explorer and macOS Finder create files empty and then upload the content. A file can still be renamed and a new one uploaded under its old name: the original stays.
- **Upstream's "Can upload"** is upstream's again: the server allows rename and move, but the apps don't offer it.
- **Resharing** isn't possible in OpenCloud 7.2 (the server refuses folder shares that carry the right to share), so Employees, like Clients, share nothing. Admins share client folders from their personal space.
- **The server doesn't enforce Preferences' permissions** (upstream: settings values are saved without a permission check), so they hide settings in the web; someone calling the API by hand could still change their own auto-accept or e-mail settings.
- **Once used, the types stay in OpenCloud's settings store,** also on an older or upstream image; their users then keep their type but get upstream's behaviour for it.

| Id | Name |
|---|---|
| `38f0649f-b600-4188-b138-fbd2c773cc43` | user type Client |
| `12047bc6-9aa3-49f6-8d78-e64b3065cd99` | user type Employee |
| `a61a37ad-42f2-46f6-a047-fe1906b525e2` | share role "Can't delete" (icon `folder-shield-2` in the built-in theme) |

## What's changed
| # | Change | For | Patch |
|---|---|---|---|
| 1 | **"Can't delete" shares report rename and move** (`NV` in `oc:permissions`) to the apps. Until 7.2.4-5 this was "Can upload"; from 7.2.4-6 "Can upload" reports upstream's letters again. No `D`, no `W`. | the share role | `patches/reva/0001-…` |
| 2 | **The web UI's Move action needs only rename permission,** no longer delete permission too. Drag and drop and cut/paste had no such check. Moving to another space is a copy, as before. | everyone (follows the share) | `patches/web/0001-…` |
| 3 | **The table is the default file view** instead of tiles. Users who already picked a view keep it (it's stored in their browser). | everyone | `patches/web/0002-…` |
| 4 | **A link to a drive that doesn't resolve leads to "Shared with me"** instead of the Spaces list (old `/files/spaces/personal` links, accounts without a personal space; from 7.2.4-7 also when a demoted user's disabled personal space is still listed). | Client, Employee | `patches/web/0003-…` |
| 5 | **The sign-in page follows the branding:** its tab title, tab icon, colours, footer and, from 7.2.4-2, a background image (see [Branding](#branding)). Without branding it looks exactly as upstream's. | everyone | `patches/server/0001-…` |
| 6 | **"Change password" can open an external password page** (from 7.2.4-4; for the optional [Authelia](deploy/README.md) setup). When `WEB_OPTION_ACCOUNT_EDIT_LINK_HREF` is set, Settings → Preferences → "Change password" opens that URL in a new tab instead of OpenCloud's dialog, and the row shows even if OpenCloud's own password change is turned off. The "Edit" button upstream shows for that setting on Settings → Profile is removed, and (from 7.2.4-5) so is the "Show devices" row `WEB_OPTION_LOGOUT_URL` would add there (see 12). Without the settings nothing changes. From 7.2.4-6 the same patch shows on Preferences only the notification settings the user's type may change (no empty tables for Clients). | everyone | `patches/web/0004-…` |
| 7 | **The share dialog finds people by username** too (from 7.2.4-5), not only by display name and e-mail. The server already matched usernames; the web dropped those results. | everyone | `patches/web/0005-…` |
| 8 | **No Spaces entry** in the left menu. Shares is their first entry and their start page anyway: they have no Personal or Favorites. (7.2.4-5 did this for everyone.) From 7.2.4-7 no Personal entry either, even for a User changed to Client or Employee, whose disabled personal space the server still lists. | Client, Employee | `patches/web/0006-…` |
| 9 | **Shares can't be hidden:** no Hide action, no eye button in "Shared with me", no Shares / Hidden Shares switch. Shares hidden before show in the list again. | Client, Employee | `patches/web/0007-…` |
| 10 | **No "Copy permanent link".** The link only ever worked for people who already had access. | Client, Employee | `patches/web/0008-…` |
| 11 | **No text or Markdown files in "New".** Such files still open in the Text Editor. (Its app menu entry needs a personal space upstream.) | Client | `patches/web/0009-…` |
| 12 | **"Log out" also signs out of an identity provider without a sign-out endpoint** (from 7.2.4-5; Authelia has none): with `WEB_OPTION_LOGOUT_URL` set, "Log out" goes there after OpenCloud's own sign-out, so the next sign-in asks for the password again and another user can sign in. [`deploy/`](deploy/README.md) sets it to Authelia's `/logout`. With OpenCloud's own sign-in nothing changes. | everyone | `patches/web/0010-…` |
| 13 | **The user types Client and Employee** among the default types (see [above](#user-types-and-the-cant-delete-role)). | – | `patches/server/0002-…` |
| 14 | **The share role "Can't delete"**, with its icon. | – | `patches/server/0003-…` |
| 15 | **The type table:** one place in the web code that says which change applies to which user type (`ocCustomFeatures` in web-pkg). | – | `patches/web/0011-…` |
| 16 | **Apps open a file for editing only with write permission** (`W`); upstream also accepted rename/move (`NV`) alone. | everyone (follows the share) | `patches/web/0012-…` |
| 17 | **Only an admin can enable a disabled personal space,** its owner too can't (upstream let a space's manager enable it, and the owner manages their personal space). Disabling one already needed an admin. `deploy/users.sh` disables a user's personal space when they become a Client, Employee or User Light. | everyone | `patches/reva/0002-…` |
| 18 | **"Can't delete" can't replace a file:** the server refuses an upload over an existing file that has content (PUT, TUS, COPY onto it; renaming or moving onto a name already needed delete permission). An empty file may still be replaced (Windows Explorer and macOS Finder create files empty first), and the same content uploaded again (the upload declares the file's SHA-1, as OpenCloud's desktop and phone apps do; from 7.2.4-14 an MD5 isn't accepted for this, since two files with the same MD5 are easy to make). Both are checked again when the upload finishes: if the file changed after the upload started (a resumable upload stays open for a day), the upload is refused (412). | the share role | `patches/reva/0003-…` |
| 19 | **No "Replace" in the upload dialog for a file that can't be replaced:** when an upload's name exists and the file reports rename/move without write (`NV` without `W`, as on "Can't delete") and has content, the dialog offers Keep both and Skip only, and a "Replace" chosen for all conflicts isn't applied to it (it gets its own question). Upstream's roles keep their dialog. | everyone (follows the share) | `patches/web/0013-…` |
| 20 | **No Text Editor in the app menu,** even for a User changed to Client or Employee: upstream shows it whenever there's a personal space, and their disabled one still counts (the entry would create a file in it). | Client, Employee | `patches/web/0014-…` |
| 21 | **A failed sign-in offers "Log in again"** (from 7.2.4-8): the "Authentication failed" page (e.g. after an account was disabled, or the session ended at the identity provider) gets a "Log in again" button, instead of leaving the user on a page without a way out. It asks the identity provider for its sign-in form (`prompt=login`): the built-in one still has the old session and would otherwise answer with the same error again. The option to ask for it is in patch 12's file (`authService.loginUser`). | everyone | `patches/web/0015-…` |
| 22 | **No notification bell in the top bar unless the branding turns it on** (from 7.2.4-12): `"notificationBell": true` in `theme.json`'s `clients.web.defaults` brings it back. OpenCloud's notifications service still sends its e-mails either way. | everyone | `patches/web/0016-…` |

**Defaults in the image** (from 7.2.4-6; a compose file can override them): `FRONTEND_DISABLE_RADICALE=true` (no Calendar in the app menu: without a CalDAV server it leads nowhere), `FRONTEND_CONFIGURABLE_NOTIFICATIONS=true` (the per-event notification table in Preferences) and, from 7.2.4-8, `WEB_OPTION_DISABLE_FEEDBACK_LINK=true` (no "Give feedback" button in the top bar: it asks users for ideas on OpenCloud's own survey site).

Everything else is upstream's: the image is the official one with only `/usr/bin/opencloud` replaced, built from the same sources and with the same toolchain and target. `opencloud version` and `status.php` report `7.2.4+custom.<build>`.

## Deploying
**A complete server behind SWAG, optionally with sign-in with 2FA (Authelia):** the [`deploy/`](deploy/) folder (compose files, Authelia's config, the SWAG site, `setup.sh` and `users.sh`). Its [README](deploy/README.md) walks through setting it up, users, 2FA, backups and updates.

**The management API (optional, from 7.2.4-9):** a small API in the image that a management tool on another machine calls through SWAG, instead of SSH to the server and OpenCloud's impersonation: Authelia's users (create, password, disable, e-mail, 2FA required or not, reset 2FA) and, for one user at a time, OpenCloud's e-mail notifications and a User's personal space. It never acts on admins. `deploy/` runs it as an add-on; see [its README](deploy/README.md#the-management-api-optional-from-724-9).

**Your own compose file:** compared with one for the official image, two lines change:
```yaml
services:
  opencloud:
    image: ghcr.io/kovacsakos14/oc-custom:7.2.4-13   # was: opencloudeu/opencloud:7.2.4
    volumes:
      - ./config:/etc/opencloud
      - ./data:/var/lib/opencloud
      # optional: this server's branding (see below)
      - ./branding:/var/lib/opencloud/web/assets/themes/_branding:ro
```
- **A new server** needs `opencloud init` once, as with the official image (without its config OpenCloud exits at once): `docker compose run --rm opencloud init --insecure false --admin-password '…'`, then `docker compose up -d`. `deploy/` does this in the compose file's entrypoint instead.
- **Update or roll back** by changing the tag, then `docker compose pull && docker compose up -d`.
- **Tags never change** once published: `7.2.4-1`, `7.2.4-2`, … each mean one build. There's no `latest`, so a server only changes when you change its tag. For extra certainty, pin the digest as well (`…:7.2.4-1@sha256:…`).
- **The image is public,** so servers pull it without logging in. It holds nothing secret: config and branding stay on each server.
- **From 7.2.4-12 to 7.2.4-13:** change the tag. A "Can't delete" upload that was started over an empty file (or with the same content) is refused when it finishes if the file changed meanwhile (412). With [`deploy/`](deploy/README.md), take its new files: `users.sh` now makes Authelia's password hashes with this image (`OC_IMAGE` must be 7.2.4-13 or later), so the password is never on a command line; the management API's `compose.manage.yaml` believes the client address only from SWAG (`SWAG_CONTAINER` in `.env` when SWAG's container isn't called `swag`).
- **From 7.2.4-11 to 7.2.4-12:** change the tag. The notification bell is gone from the top bar; to keep it, add `"notificationBell": true` to `clients.web.defaults` in the branding's `theme.json` (patch 22). OpenCloud's e-mails don't change.
- **From 7.2.4-10 to 7.2.4-11:** change the tag; the image is the same as 7.2.4-9's. With [`deploy/`](deploy/README.md), take its new compose files and run `./setup.sh` (it makes `authelia/assets/` and `manage-api/state/`), then `docker compose up -d`. Authelia now serves its sign-in page's logo and tab icon from `authelia/assets/` (empty: its own), and the management API can write the branding folder and that one ([Branding through the API](deploy/README.md#the-management-api-optional-from-724-9)). The API's records move from a Docker volume to `manage-api/state/`: update when no new user was made in the last few minutes, or e-mails it muted for that user stay off.
- **From 7.2.4-9 to 7.2.4-10:** change the tag; the image is the same as 7.2.4-9's. With [`deploy/`](deploy/README.md), take its new files and run `./setup.sh`: it rewrites the SWAG site, which now adds a ["Back to your files"](deploy/README.md#notes-and-troubleshooting) button to Authelia's settings pages (where users are left after registering 2FA or changing their password); copy it to SWAG again and restart SWAG. `MANAGE_API_IMAGE` (optional) runs the management API from another image.
- **From 7.2.4-8 to 7.2.4-9:** change the tag; nothing else changes on its own. The image now also holds the optional [management API](deploy/README.md#the-management-api-optional-from-724-9) and Authelia's CLI for it. With [`deploy/`](deploy/README.md), take its new files: they add the API as an add-on (`compose.manage.yaml`, `compose.manage-authelia.yaml`, a `/manage-api/` location in the SWAG site, its token from `setup.sh`), and `authelia/configuration.yml` asks the new `2fa` group for 2FA too (copy it over yours if you want 2FA for non-admins: `users.sh 2fa`, the API's `two_factor`; both refuse until it's there). Then run `./setup.sh` and copy the rewritten SWAG site to SWAG again.
- **From 7.2.4-7 to 7.2.4-8:** change the tag. The feedback button in the top bar is gone (`WEB_OPTION_DISABLE_FEEDBACK_LINK=true` in the image; a compose file can set it to `false`), and the "Authentication failed" page has a "Log in again" button.
- **From 7.2.4-6 to 7.2.4-7:** change the tag. With [`deploy/`](deploy/README.md), also take its new files, run `./setup.sh` and copy the rewritten SWAG site to SWAG again; `OC_ADMIN_PASSWORD` can then be emptied in `.env`; impersonation is off unless `.env` sets `AUTH_APP_ENABLE_IMPERSONATION=true`; the first `users.sh` run removes an old `.admin-token`, whose token should be deleted under admin's App Tokens. Users changed to Client or Employee keep their personal space but disabled (`users.sh role`).
- **From 7.2.4-5 to 7.2.4-6:** give client accounts the Client type and staff accounts the Employee type, and share client folders as "Can't delete" (existing "Can upload" shares lose rename/move in the apps). Accounts left as User or User Light get upstream's web UI.

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
| `clients.web.defaults.notificationBell` | `true` shows the notification bell in the top bar (from 7.2.4-12; hidden without it) |

- **Logos** are SVG or PNG files in the same folder, referenced as `themes/_branding/<file>`.
- **Colours:** `clients.web.themes` holds the light and dark theme. It's a list, and a list replaces upstream's as a whole, so copy both entries, as `theme.colors.example.json` does, and change the `primary`/`chrome` tokens. A theme entry can also set its own `logo` (the example uses the white logo in dark mode).
- **The sign-in page's background image** (`common.signin.backgroundImage`): a file in the folder, referenced like the logos. Only paths on the server itself are used (from 7.2.4-3; no `https://` URLs, `%` escapes or `..`), so the sign-in page loads nothing from other sites; the same goes for the favicon. For an image elsewhere, upstream's `IDP_LOGIN_BACKGROUND_URL` still works. It covers the page (scaled to fill, centred) and replaces the hexagon; `common.signin.background` still shows while it loads, so pick a matching colour. Use a JPEG or WebP of about 1920×1080 and a few hundred KB. Remove the key to get the plain background back. Upstream's environment setting `IDP_LOGIN_BACKGROUND_URL` still works and wins over the key.
- **The sign-in page keys are ours** (patch 5): upstream's sign-in page ignores the theme apart from the logo. Colours accept CSS colour values (`#rrggbb`, `rgb(…)`, names). Keys you leave out keep upstream's look.
- **Read-only mount:** the folder is the only source of truth. OpenCloud's own logo upload API (`/branding/logo`, which the 7.2 web UI doesn't use) can't change it. Edit it on the host (by hand, or from 7.2.4-11 through the [management API](deploy/README.md#the-management-api-optional-from-724-9), which checks every file it writes); OpenCloud's container only reads it.
- **Authelia's sign-in page** (with [`deploy/`](deploy/README.md)'s Authelia, from 7.2.4-11) takes its logo and tab icon from `authelia/assets/` instead: `logo.png` (PNG only) and `favicon.ico` (ICO only), shown at once. Its texts can be changed there too: `locales/<lang>/portal.json` replaces Authelia's whole file for that language (a Hungarian browser asks for `hu-HU` before `hu`).
- **Source link (AGPL):** link this repository from the imprint page, or add another entry under `common.urls`.

## Building
The build needs more memory than a small VM has (the Go link step alone takes several GB), so it runs in **GitHub Actions** (`.github/workflows/build.yml`):
- **A tag `v7.2.4-<build>`** builds, smoke-tests and publishes `:7.2.4-<build>`, then creates a GitHub release for the tag (the image name, then the PRs merged since the last release). The tag must match `OC_VERSION` and `BUILD` in `versions.env`, and an existing image tag is never overwritten. A failed build creates no release.
- **A manual run** (Actions → Build → Run workflow) publishes `:test-<commit>` for trying a build before tagging it.

`scripts/build-native` is what CI runs, and it works on any machine with Docker and about 8 GB of RAM:
1. Fetch the upstream sources at the pinned versions and apply the patches.
2. Build the patched web, cached in `out/web-dist` and rebuilt only when its version or patches change. Every build image is pinned by digest, and pnpm by version and hash (`PNPM_VERSION`, `PNPM_SHA512` in `versions.env`; the build adds the hash to the web's `packageManager`, so corepack checks the download, and stops if upstream pins another pnpm).
3. Build the IDP frontend (patched), then the server binary in upstream's `golang-ci` image with the official image's target (`release-linux-docker-amd64`, libvips). The server vendors its dependencies, so the reva patches go into its vendored reva (the same version as `REVA_VERSION`).
4. Vet, test and build the management API (`manage-api/`, Go, static), and fetch Authelia's own musl build of the pinned version for it (`AUTHELIA_MUSL_URL`, checked against `AUTHELIA_MUSL_SHA256`: the Authelia image's binary needs glibc, and this image is Alpine).
5. Copy the binaries into the official image (`Dockerfile`), and check that they run and that OpenCloud finds its libraries.

Manual runs keep caches (pnpm store, Go modules and build cache, the web) between runs; a release (tag) builds from nothing, so nothing another run left in a cache can end up in it. Only amd64 is built.

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
- **Our own rebuilds** (`7.2.4-1` → `7.2.4-2`) only change the patches, not stored data: switch the tag back. Going back before 7.2.4-6 leaves the Client and Employee types stored and assigned (with upstream's behaviour), and "Can't delete" shares then show as an unnamed set of permissions.
- **Patch releases** (`7.2.4` → `7.2.5`) normally don't change data formats; read the release notes.
- **Minor or major upgrades** (7.2 → 8.x) are one-way. Before upgrading, stop the container and snapshot or copy `/var/lib/opencloud` and `/etc/opencloud`. To roll back, restore both together with the old tag. Anything users changed since the upgrade is lost by the restore.

## Testing
Builds are tested in a separate OpenCloud test environment (Docker Compose, not part of this repo) with test users of each role and folder shares of each kind. The helpers in `test/` work with any such environment: copy `test/local.env.example` to `test/local.env` (gitignored) and set its compose command, URL and admin app token.
- `test/use-custom <image> [branding dir]` runs the test environment's OpenCloud on another image, with a branding folder (default `branding/example`); `test/use-custom --official` runs upstream's image of the same version without branding.
- `test/type-users` creates a Client (`typeclient`) and an Employee (`typeemployee`) and an admin folder `zz-types` shared to them as "Can't delete" and "Can edit"; it prints their passwords for `test/browser`. `--remove` deletes them again.
- `test/browser <out dir> user:password …` signs in as each user in a fresh browser profile. It saves screenshots and records where they landed, whether files show as a table, the left menu, the app menu and what "Shared with me" offers (hide buttons, the hidden-shares switch). With `ACTIONS="client1:Client folder/readme.txt"` it also opens that file's context menu and lists its actions (rename, move, delete…); with `NEW=1` it lists the "New" menu in Personal and in those shares; with `OPEN="client1:Client folder/readme.txt"` it opens the file in its app and records whether it's editable; with `PREFS=1` it lists Preferences' notification sections; with `SEARCH="admin:client1"` it types into the share dialog and lists who it offers.
- `test/check-permissions <user> <share>` lists what OpenCloud reports for each item in a share (`oc:permissions`) as the recipient sees it. A "Can't delete" share shows `NV` (root `SNVCK`), "Can upload" upstream's `SCK` and nothing on files.
- `scripts/smoke-test <image>` starts an image with a fresh config and checks the version, the branding, the web UI, the user types, the share role and its icon, and the image's defaults (CI runs it on every build).
- `test/deploy/run.sh [--image <image>] [--keep]` tests `deploy/` from scratch, about 10 minutes, Docker only: the real SWAG image (with a self-signed certificate; Let's Encrypt is skipped) and Mailpit on their own network, then the stack with Authelia and without it. It checks `setup.sh` and every `users.sh` command (also creating Clients and Employees), and in a browser: a user signing in with a password only, "Change password" opening Authelia's page and changing the password there, a disabled user refused, the desktop app's sign-in (PKCE, its loopback redirect, refresh), the admin registering 2FA at the first sign-in, removing and re-registering it on Authelia's settings page and after `users.sh reset-2fa`; without Authelia, OpenCloud's own sign-in and password dialog. With the management API on both stacks: its token through SWAG, an Authelia entry created, disabled and given a new password through it (and signing in with it), Reset 2FA, muting e-mails and a personal space with impersonation off, no token left behind, every change to an admin refused, `users.sh` and the API changing Authelia's file at the same time, the API following an OpenCloud restart, and a new token from `setup.sh`. `--keep` leaves it running; `--clean` removes it.
- Unit tests: each patch brings its own (Go for server and reva, vitest for the web; reva's need `golang:1.25-alpine`). The build doesn't run them; see `CLAUDE.md`.

**What to check for each new build** (`test/type-users`, then `test/browser` with `NEW=1 PREFS=1` and `ACTIONS`/`OPEN` on `zz-types/note.txt`, plus an upstream User and the admin):
- **A Client** lands on "Shared with me"; the menu is Shares and Deleted files; no hide buttons or switch; "New" in the share has no text files; the file's menu has Move and Rename but no Delete and no "Copy permanent link"; the file opens read-only; Preferences shows no notification sections.
- **An Employee:** the same, except text files in "New", Delete on "Can edit" files, the file opens editable, and Preferences shows the notification table.
- **An upstream User and the admin:** upstream's web (land on Personal, Spaces, hide buttons, Text Editor in the menus, the notification table).
- **Permissions:** `test/check-permissions` for "Can't delete" (`NV`), "Can upload" (upstream's) and "Can edit" (unchanged); a public upload link has no rename/move.
- **The share dialog** offers "Can't delete" with its icon, and typing a username finds the user.
- **The branding:** it shows on the sign-in page and in the web UI. Without a branding folder, the sign-in page looks like upstream's.
- **The test environment's own end-to-end tests still pass** (migration, user and share management).

## Layout
- `versions.env`: the upstream versions, the base image and our build number.
- `patches/server`, `patches/reva`, `patches/web`: the changes, as plain diffs. No two patches touch the same file.
- `scripts/fetch`: clone upstream at those versions into `upstream/` (gitignored).
- `scripts/apply`: apply the patches.
- `scripts/check-upstream`: check that `upstream/` holds exactly the patches, nothing older or extra (`build-native` runs it; `OC_CUSTOM_ALLOW_DIRTY=1` makes extra changes a warning).
- `scripts/refresh-patches`: rewrite the patches from `upstream/`.
- `scripts/compat`: check that the patches apply to other versions.
- `scripts/build-native`, `Dockerfile`: the build.
- `scripts/smoke-test`: the image check.
- `branding/example`: an example branding folder.
- `deploy/`: a complete server behind SWAG, with or without Authelia, optionally with the management API (its own README).
- `manage-api/`: the management API (Go), in the image as `manage-api`, with Authelia's CLI as `authelia`.
- `test/`: the test helpers (`local.env.example` for your test environment); `test/deploy/`: the test of `deploy/`.
- `CLAUDE.md`: working notes for continuing development (decisions, findings, next steps).

## Status
**Released:** `7.2.4-13` (2026-10-30; `sha256:c2930ea8…`): security fixes. A "Can't delete" upload started over an empty file (or with the same content) is refused when it finishes if the file changed meanwhile; the management API and `users.sh` make Authelia's password hashes without the password on a command line (`users.sh` needs this image); the management API believes `X-Real-IP` only from SWAG (`SWAG_CONTAINER`). `7.2.4-12` (2026-10-29; `sha256:d326e757…`): no notification bell in the top bar unless the branding turns it on (patch 22); otherwise the same as `7.2.4-11`. `7.2.4-11` (2026-10-28; `sha256:b8b9f7e2…`): the same image as `7.2.4-9`; `deploy/` lets the management API write the branding folder and Authelia's new asset folder (`authelia/assets/`: its sign-in page's logo and tab icon), and keeps the API's records in `manage-api/state/`. `7.2.4-10` (2026-10-28; `sha256:802551a3…`): the same image as `7.2.4-9`; `deploy/` adds a "Back to your files" button to Authelia's settings pages and `MANAGE_API_IMAGE`. `7.2.4-9` (2026-10-28; `sha256:5afe16e9…`): the optional [management API](deploy/README.md#the-management-api-optional-from-724-9) in the image (with Authelia's CLI) and in `deploy/` as an add-on, and 2FA for chosen non-admin users (Authelia's `2fa` group, `users.sh 2fa`); OpenCloud itself is the same as in `7.2.4-8`. `7.2.4-8` (2026-10-27; `sha256:65973cf3…`): no feedback button in the top bar by default, and the "Authentication failed" page offers "Log in again" (patch 21); otherwise the same as `7.2.4-7`. `7.2.4-7` (2026-10-27; `sha256:ddfbd25b…`): fixes from a review of the whole repo: "Can't delete" can't replace a file with content (the server refuses it, the upload dialog has no Replace), only an admin can enable a disabled personal space, Clients and Employees see no Personal or Text Editor entry even with a disabled one (patches 17–20); `deploy/` hardened (impersonation opt-in, a short-lived token, the admin password only for `init`, Authelia sees the real client address, a stricter `.env` and SMTP check) and `users.sh` faster. `7.2.4-6` (2026-10-26; `sha256:a6959387…`): the user types Client and Employee and the share role "Can't delete" (patches 13–16); the file-server changes of 7.2.4-5 now apply to Clients and Employees only (Admins, Users and User Light get upstream's web again), "Can upload" is upstream's again, and Calendar off and the notification table are image defaults. `7.2.4-5` (2026-10-22; `sha256:e313341d…`): patches 7–12 (username search, Shares first and no Spaces, no hiding of shares, no permanent link for User Light, the Text Editor out of the menus, "Log out" through Authelia's `/logout`) and Calendar off in `deploy/`; otherwise the same as `7.2.4-4`. `7.2.4-4` (2026-10-22): patch 6 (["Change password" can open an external password page](#whats-changed)), for the optional Authelia setup in [`deploy/`](deploy/README.md), tested with Authelia 4.39.28 (`AUTHELIA_IMAGE` in `versions.env`); otherwise the same as `7.2.4-3`. `7.2.4-3` (2026-10-21): the sign-in page takes background images and the favicon only from the server itself (no `https://` URLs, `%` escapes or `..`); the web is built with a pinned node image and pnpm, releases without caches. `7.2.4-2` (2026-10-21): adds the sign-in background image (`common.signin.backgroundImage`) to patch 5; otherwise the same as `7.2.4-1`.

`7.2.4-1` (2026-10-21), OpenCloud 7.2.4 with patches 1–5. Tested:
- what a "Can upload" recipient is told and can do (rename and move yes, delete no);
- public upload links unchanged;
- landing pages and the table view in a fresh browser;
- the context menu actions;
- the branding on the sign-in page and in the web UI;
- the test environment's end-to-end tests (migration, user and share management).

**Upstream compatibility** (`scripts/compat`, 2026-10-26):

| OpenCloud | Patches |
|---|---|
| 7.2.4, stable-7.2 (the coming 7.2.5) | All apply |
| 8.1.0 (rolling) | Server, reva, `web/0001`, `0002`, `0008`, `0011` and `0012` apply; `web/0003`–`0007`, `0009` and `0010` conflict (web 8.x changed those files) and need a rebase |

**Optional sign-in with 2FA through Authelia (from 7.2.4-4).** OpenCloud's built-in sign-in has no two-factor authentication. oc-custom offers [Authelia](https://www.authelia.com/) (Apache-2.0) as an optional, tested companion ([`deploy/`](deploy/README.md)):
- **Without Authelia nothing changes:** OpenCloud's built-in sign-in and its own "Change password" dialog.
- **With Authelia:** it runs in its own container in the same compose stack (not inside this image: it has its own security releases, data and process), under `https://<OpenCloud's name>/authelia`, so one DNS name and one certificate. OpenCloud runs with its built-in IdP off, without autoprovisioning and with the `default` role driver. Users still live in OpenCloud's own directory; Authelia's file backend holds their passwords and groups, its database their 2FA devices. 2FA (TOTP) is required for admins, and (from 7.2.4-9) for other users an admin puts in the `2fa` group (`users.sh 2fa`, the management API); users can add or remove their own authenticator app on Authelia's settings page.
- **A tested pair:** `versions.env` pins the Authelia version (`AUTHELIA_IMAGE`) each build is tested with, and `test/deploy/run.sh` tests the pair behind the real SWAG image.
- **Patch 6 (web):** Settings → Preferences → "Change password" opens the URL in `WEB_OPTION_ACCOUNT_EDIT_LINK_HREF` (Authelia's password page) in a new tab, and the "Edit" button that setting would add to Settings → Profile is removed.
- **Patch 12 (web, from 7.2.4-5):** Authelia 4.39 has no OIDC sign-out endpoint, so OpenCloud's "Log out" used to leave Authelia's session behind and the next sign-in skipped the password. With `WEB_OPTION_LOGOUT_URL` set to Authelia's `/logout?rd=<the server>`, "Log out" ends both.
- **SWAG:** the site `deploy/` writes proxies `/authelia` to Authelia and the rest to OpenCloud. SWAG's own Authelia files are for protecting other apps and aren't used. SWAG's `proxy.conf` turns `http://` redirects into `https://`, which broke the desktop app's sign-in (it ends with a redirect to `http://127.0.0.1:<port>`); the site keeps those unchanged.

**Next:**
- **7.2.5:** decide whether it's worth a rebuild once it's released.
- **The next production release** (due mid-November 2026, probably 8.x): rebase the web patches that conflict (see the table), check that 8.x's new default user-type permissions and share roles leave ours unique, test, and snapshot servers before upgrading, since 7.2 → 8 is one-way.

## Licence
- **Upstream:** OpenCloud's server and reva are Apache-2.0; OpenCloud Web is AGPL-3.0.
- **This repository** (the patches and scripts) is AGPL-3.0; see `LICENSE`.
- **Corresponding source:** anyone using an instance running this build can get its source here. That's upstream at the versions in `versions.env` (each image tag `X-N` matches the git tag `vX-N`) plus `patches/`.
- **Trademark:** "OpenCloud" is a trademark of OpenCloud GmbH. This build is not affiliated with them.
