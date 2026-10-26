# Deploying oc-custom behind SWAG

This folder is a complete OpenCloud server: oc-custom, optionally [Authelia](https://www.authelia.com/) for sign-in with two-factor authentication (2FA), and a site for [SWAG](https://docs.linuxserver.io/general/swag/) in front. Copy it to the server, fill in `.env`, run `./setup.sh`, start it.

| | With Authelia (default) | Without Authelia |
|---|---|---|
| Sign-in page | Authelia's, at `https://<your domain>/authelia` | OpenCloud's own |
| 2FA | Required for admins (an authenticator app); others sign in with their password | None |
| Passwords live in | Authelia's user file (`authelia/users/`) | OpenCloud |
| "Change password" in OpenCloud | Opens Authelia's page (password, and the user's own 2FA) | OpenCloud's dialog |
| "Log out" in OpenCloud | Also ends the Authelia session, so the next sign-in asks for the password | OpenCloud's own |
| Containers | `opencloud`, `authelia` | `opencloud` |

Users are always created in OpenCloud. With Authelia they also need an entry in Authelia's user file; `./users.sh` does both.

## What you need
- A Linux server with Docker and Docker Compose 2.24 or newer.
- SWAG running on the same server, with a certificate that covers the domain (e.g. `SUBDOMAINS=cloud` for `cloud.example.com`).
- A DNS record for the domain pointing at the server. Authelia lives under `/authelia` on the same domain, so one record is enough.
- For e-mail: an SMTP account. Without one everything works, but Authelia writes its one-time codes to a file instead of e-mailing them (see "Without SMTP").

## Setting up
1. **Copy this folder** to the server, e.g. as `~/docker/opencloud1`, as the user that should own the data (not root; it must be in the `docker` group).
2. **Settings:** `cp .env.example .env` and edit `.env`:
   - `OC_STACK`: a short name, unique on this server (the containers are called `<OC_STACK>-opencloud` and `<OC_STACK>-authelia`);
   - `OC_DOMAIN`: the domain, e.g. `cloud.example.com`;
   - `OC_ADMIN_PASSWORD` and `ADMIN_EMAIL`: the admin's password and e-mail address;
   - `SWAG_NETWORK`: the Docker network SWAG is on (`docker network ls`; often `swag_default` or a network you created for SWAG);
   - `SMTP_*`: the mail account, or leave `SMTP_HOST` empty;
   - without Authelia: delete the `COMPOSE_FILE=` line.
3. **`./setup.sh`**: checks `.env`, creates the folders and Authelia's secrets, writes Authelia's user file with the admin, and writes the SWAG site `swag/<OC_STACK>.subdomain.conf`. It never replaces what exists, so it's safe to run again.
4. **SWAG:** copy `swag/<OC_STACK>.subdomain.conf` into SWAG's `config/nginx/proxy-confs/`, then `docker restart swag` (or whatever your SWAG container is called). Don't add SWAG's own `authelia-*.conf` includes to this site: those are for protecting other apps with Authelia, not for this.
5. **Start:** `docker compose up -d`. The first start takes a minute (OpenCloud creates its config).
6. **Sign in** at `https://<your domain>` as `admin` with `OC_ADMIN_PASSWORD`. Then keep the password elsewhere and empty `OC_ADMIN_PASSWORD` in `.env`: only the first start uses it (`setup.sh` hands it over in `config/.admin-password`, which OpenCloud deletes after creating its configuration), and it stays out of the container's environment. With Authelia, the admin is asked to register an authenticator app (e.g. Google Authenticator, Aegis, 1Password) first: Authelia e-mails a one-time code to `ADMIN_EMAIL` to confirm it's you, then shows a QR code.

The desktop and mobile apps connect to `https://<your domain>` as usual. They open the sign-in in the browser; with Authelia there's a consent page once when an account is added ("allow OpenCloud Desktop to access your account").

## Users
```
./users.sh list
./users.sh add anna --name "Anna Kovács" --email anna@example.com --role employee   # asks for the password
./users.sh add client1 --name "Client One" --email c1@example.com --role client --random
./users.sh passwd anna                     # a new password (asks, or --random)
./users.sh role anna admin                 # user | admin | light | client | employee
./users.sh email anna anna@example.org
./users.sh disable anna / enable anna
./users.sh delete anna                     # deletes her files too; asks to confirm
./users.sh reset-2fa anna                  # lost phone: removes her 2FA devices
```
- A password can come from stdin instead of the prompt: `echo 'secret' | ./users.sh passwd anna`. `--random` makes one up and shows it once.
- **Roles:** `client` and `employee` are oc-custom's file-server types (no personal space; clients get folders shared as "Can't delete", staff as "Can edit"; see the [main README](../README.md#user-types-and-the-cant-delete-role)). `user`, `admin` and `light` are upstream's (User, Admin, User Light) with upstream's web UI. `add` without `--role` makes a `user`.
- **Admins** (`--role admin`) are in Authelia's `admins` group, so they need 2FA. Changing the role with `users.sh role` changes both.
- With Authelia, OpenCloud's own password for each user is random and unused; passwords are checked by Authelia only.
- `users.sh` uses an app token for `admin` that it makes at each run, keeps in memory and lets expire after 10 minutes. Older versions kept one in `.admin-token` for a year; `users.sh` removes that file, and the old token should be deleted under the admin's Settings → App Tokens.
- Impersonation (an admin's app token making tokens for other users) is off; `AUTH_APP_ENABLE_IMPERSONATION=true` in `.env` turns it on for management tools that need it. `users.sh` doesn't.
- Users created in OpenCloud's web UI (Admin settings → Users) exist only in OpenCloud. With Authelia they can't sign in until they have an entry in Authelia's file: create them with `users.sh` instead.

**What users see:** Clients and Employees land on "Shared with me"; their menu is Shares and Deleted files, with no Spaces entry. Admins and upstream's types see upstream's web (they land on Personal). Nobody has Calendar in the app menu (the image's default).

**What users do themselves (with Authelia):** in OpenCloud, their initials (top right) → Preferences → "Change password" opens Authelia's settings in a new tab. There they change their password, and in the menu (☰) → Two-Factor Authentication they add or remove their authenticator app. Authelia first e-mails them a one-time code to confirm it's them.

**Lost phone:** `./users.sh reset-2fa <user>`. The user signs in with their password; an admin is then asked to register a new app straight away.

## Without SMTP
Authelia writes each one-time code (registering 2FA, changing a password, "Reset password" on the sign-in page) to `authelia/data/notification.txt` instead of e-mailing it; the newest is at the end:
```
tail -20 authelia/data/notification.txt
```
OpenCloud sends no e-mails at all (no share notifications).

## Backups
Back up the whole folder. With the stack stopped (`docker compose stop`) the copy is consistent:
- `config/`, `data/`: OpenCloud's configuration and all files;
- `authelia/`: the user file (passwords), the database (`authelia/data/db.sqlite3`: the users' 2FA devices) and the secrets (`authelia/secrets/`: without them the database can't be read);
- `.env`.

## Updating
Each oc-custom release names the OpenCloud image and the Authelia version it was tested with (`versions.env` in the repo). Change `OC_IMAGE` and `AUTHELIA_IMAGE` in `.env` to those, then:
```
docker compose pull && docker compose up -d
```
Take a backup first when the release notes say an update is one-way (e.g. OpenCloud 7 → 8).

## Notes and troubleshooting
- **Logs:** `docker compose logs -f opencloud`, `docker compose logs -f authelia`.
- **A broken user file stops Authelia.** `users.sh` checks the file before replacing it. If you edit `authelia/users/users_database.yml` by hand, keep it valid YAML; Authelia reloads it within seconds (`docker compose logs authelia` shows "Reloaded successfully").
- **Too many attempts:** Authelia rate-limits one-time code requests and bans a user for a few minutes after repeated wrong passwords. `docker compose restart authelia` clears both.
- **The SWAG site keeps redirects to `http://127.0.0.1` unchanged** (SWAG's `proxy.conf` would turn them into `https://`): the desktop app's sign-in ends with one. Keep those two `proxy_redirect` lines if you edit the site.
- **OpenCloud reaches its own domain through SWAG** (`extra_hosts: <domain>:host-gateway` in `compose.yaml`), so SWAG must listen on this server's port 443.
- **Switching between the variants later:** the users' passwords live in different places (OpenCloud or Authelia), so everyone needs a new password after a switch (`users.sh passwd`, which also creates the missing Authelia entry).
