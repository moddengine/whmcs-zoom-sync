# WHMCS Zoom Sync

Nightly sync of active WHMCS clients into the Zoom Phone external contact list.
Phone numbers are validated and normalized to E.164, using Australia as the
default region.

Contacts are matched by WHMCS client ID and created or updated with the client's
name, company, email and phone number. Invalid numbers and numbers already owned
by another client are skipped. Contacts are never deleted when a WHMCS client
becomes inactive. Each run currently fetches at most 5,000 active clients.

## Configuration

Copy the example environment file and add your credentials:

```sh
cp .env.dist .env
chmod 600 .env
```

Edit `.env` with the following values. All six API settings are required:

| Setting | Description |
| --- | --- |
| `WHMCS_API_URL` | Full WHMCS API endpoint, e.g. `https://billing.example.com/includes/api.php`. |
| `WHMCS_IDENTIFIER` | WHMCS API credential identifier. |
| `WHMCS_SECRET` | Secret for that WHMCS API credential. |
| `ZOOM_ACCOUNT_ID` | Account ID from a Zoom Server-to-Server OAuth app. |
| `ZOOM_CLIENT_ID` | Client ID from the same Zoom app. |
| `ZOOM_CLIENT_SECRET` | Client secret from the same Zoom app. |
| `TZ` | Container scheduler timezone; Compose sets `Australia/Melbourne`. |

The WHMCS API credentials must allow `GetClients` and `GetClientsDetails`, and
the host's outbound IP must be allowlisted in WHMCS. The Zoom Server-to-Server
OAuth application must be activated and have permission to list, create and
update Zoom Phone external contacts for the account.

Keep `.env` private; it is excluded from Git and the container build. The binary
also reads `.env` from its working directory, but existing environment variables
take precedence. There is no dry-run mode: a manual run writes contacts to Zoom.

## Docker

Requires Docker Engine and Docker Compose. Clone this repository, configure
`.env` as above, then build and start the service:

```sh
docker compose up -d --build
docker compose logs -f
```

The container uses Supercronic to run the sync every night at 2:30 AM in the
`Australia/Melbourne` timezone. Starting it waits for the next scheduled run.
Change `TZ` in `docker-compose.yml` to use another timezone. To change the
schedule, edit `crontab` and rebuild the image. The service runs as a non-root
user with a read-only filesystem; no ports or persistent volumes are needed.

Run a sync immediately:

```sh
docker compose run --rm \
  --entrypoint /usr/local/bin/whmcs-zoom-sync \
  whmcs-zoom-sync
```

To update a source deployment, run `git pull` followed by
`docker compose up -d --build`. Stop it with `docker compose down`. Check the
logs for completion or API errors; failed contacts make the run exit nonzero,
and the scheduler attempts another run at the next scheduled time.

## Deploy the released image

Release images are published for Linux amd64 at
`ghcr.io/moddengine/whmcs-zoom-sync`. To use `v0.1.0` without building locally,
create `docker-compose.override.yml` alongside `docker-compose.yml`:

```yaml
services:
  whmcs-zoom-sync:
    image: ghcr.io/moddengine/whmcs-zoom-sync:v0.1.0
```

Then pull and start the image with the same `.env` settings:

```sh
docker compose pull
docker compose up -d --no-build
```

For upgrades, change the image tag, then repeat those commands. If the GHCR
package is private, authenticate with `docker login ghcr.io` using a GitHub
personal access token (classic) with `read:packages`, or make the package public
in its GitHub package settings.

## CI and releases

GitHub Actions runs `go test ./...` and builds the container on pull requests and
pushes to `main`. Pushing a tag matching `v*.*.*` also publishes the image to
GHCR with that exact tag, using the workflow's `GITHUB_TOKEN` and
`packages: write` permission. No WHMCS or Zoom secrets are needed in CI.
The workflow follows GitHub's [container publishing workflow](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images).

Create a release from a tested commit containing the workflow:

```sh
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin main
git push origin v0.1.0
```

Watch the repository's Actions tab for the build to finish before deploying.

## Local development

Requires Go 1.26 or later.

```sh
go test ./...
go run .
```
