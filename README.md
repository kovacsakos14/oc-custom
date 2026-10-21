# oc-custom

[OpenCloud](https://github.com/opencloud-eu/opencloud) with a few small patches, built as a drop-in replacement for the official image. This is an unofficial build, not made or supported by OpenCloud GmbH.

Based on OpenCloud **7.2.4** (reva v2.46.8, web v7.1.5); see `versions.env`.

## What's changed
1. **"Can upload" shares can rename and move items.** The server already accepts the move, but didn't report it to the apps (`oc:permissions` had no `NV`), so the web UI and the desktop app didn't offer it. Delete and editing stay off. Public links are unchanged.
2. **The web UI's Move action needs only rename permission,** no longer delete permission too, so "Can upload" users get it as well.
3. **The table is the default file view** instead of tiles. Users who already picked a view keep it.
4. **Accounts without a personal space (User Light) land on "Shared with me"** instead of the empty Spaces list.

## Layout
- `versions.env`: the upstream versions and the base image.
- `patches/reva`, `patches/web`: the changes, as plain diffs.
- `scripts/fetch`: clone upstream at those versions into `upstream/` (gitignored).
- `scripts/apply`: apply the patches there.
- `scripts/refresh-patches`: rewrite the patches from `upstream/` after fixing a conflict.

## Licence
- OpenCloud's server and reva are Apache-2.0; OpenCloud Web is AGPL-3.0.
- This repo (the patches and scripts) is AGPL-3.0; see `LICENSE`.
- Anyone using an instance running this build can get its source here: upstream at the versions in `versions.env`, plus `patches/`.
- "OpenCloud" is a trademark of OpenCloud GmbH.
