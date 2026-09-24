# VRChat Image Converter Bot

A Discord bot that turns the photos you drop into a channel into direct image URLs that VRChat image viewers accept.

Upload a photo (for example an iPhone `IMG_1234.HEIC`) to an enabled channel. The bot replies with:

````text
Converted: 3024×4032 → 1125×1500 · JPEG · 846 KiB

```
https://cdn.discordapp.com/attachments/.../converted-a8f31c.jpg?ex=...
```
URL expires in 23 hours

[Refresh URL]
````

Copy the URL out of the code block and paste it into the VRChat world.

What the bot does to every image:

- **Formats:** reads JPEG, PNG, WebP and HEIC/HEIF (also AVIF, GIF and TIFF). The format is detected from the file contents, not from the filename or MIME type. Animated images keep their first frame only.
- **Rotation:** applies EXIF orientation by rotating the actual pixels.
- **Colour:** converts to 8-bit sRGB, honouring embedded ICC profiles such as Display P3.
- **Size:** shrinks to fit the configured maximum width and height, keeping the aspect ratio. It never crops or upscales.
- **Output format:** always JPEG or PNG. Transparent images become PNG when *preserve transparency* is on. Otherwise transparency is flattened onto white and the result is a JPEG.
- **Metadata:** strips EXIF, GPS and camera metadata by default.
- **File size (optional):** meets a size limit by lowering JPEG quality (binary search, minimum quality 40). If that's not enough it shrinks the dimensions, down to a 64 px minimum.

## 1. Create the Discord application

1. Open the [Discord Developer Portal](https://discord.com/developers/applications) and click **New Application**.
2. Under **Bot**:
   - Click **Reset Token** and copy the token. It goes into `DISCORD_BOT_TOKEN`. Never commit it.
   - Under **Privileged Gateway Intents**, turn on **Message Content Intent**. The bot needs it to see attachments on other people's messages.
3. Under **OAuth2 → URL Generator**:
   - Scopes: `bot`, `applications.commands`
   - Bot permissions: **View Channels**, **Send Messages**, **Send Messages in Threads**, **Attach Files**, **Read Message History**, **Add Reactions**, **Use Application Commands**
   - Also grant **Manage Messages** if you plan to enable `/config delete-original` (see below) — it's required for the bot to delete users' original messages. Skip it otherwise.
   - Open the generated URL and invite the bot to your server.

The bot doesn't need Administrator or Manage Channels. It only deletes your original messages if `/config delete-original` is turned on (default off); without **Manage Messages**, enabling that setting will fail to delete messages (logged as a warning, everything else keeps working).

Gateway intents used: `GUILDS`, `GUILD_MESSAGES` and `MESSAGE_CONTENT` (privileged).

## 2. Configure the environment

```sh
cp .env.example .env
# edit .env and set DISCORD_BOT_TOKEN
```

| Variable | Default | Purpose |
|---|---|---|
| `DISCORD_BOT_TOKEN` | *(required)* | Bot token |
| `DATABASE_PATH` | `data/bot.db` (`/data/bot.db` in Docker) | SQLite file holding guild settings |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `WORKER_COUNT` | `2` | Images converted in parallel |
| `WORK_QUEUE_SIZE` | `32` | Pending images before new ones are rejected |
| `MAX_INPUT_FILE_SIZE` | `52428800` (50 MiB) | Largest attachment the bot downloads |
| `MAX_INPUT_PIXELS` | `100000000` | Largest image (width × height) the bot decodes |
| `HEALTH_ADDR` | `:8080` in Docker, off elsewhere | Serves `GET /healthz` |
| `DISCORD_DEV_GUILD_ID` | *(empty)* | If set, registers commands to that one server, which updates instantly. Leave empty in production to register global commands. |

Guild settings such as dimensions and quality aren't environment variables. You manage them with `/config` in Discord, and they're stored per server in SQLite.

## 3. Run with Docker Compose

```sh
mkdir -p data
sudo chown 10001:10001 data   # the container runs as non-root UID 10001
docker compose up -d
docker compose logs -f
```

The SQLite database is at `./data/bot.db`, so settings survive restarts and rebuilds. `docker compose stop` sends SIGTERM. The bot then stops taking new work, lets running conversions finish (up to 20 s), and closes the database.

### Prebuilt image

`compose.yml` pulls `ghcr.io/nekogravitycat/image-converter-bot:latest` by default — no local build needed, just `docker compose pull && docker compose up -d`. CI publishes images to GHCR: a `v1.2.3` git tag publishes `1.2.3`, `1.2` and `latest`; every push to `main` updates `edge` and a `sha-<short>` tag. Pick a different tag with `IMAGE_TAG` (e.g. in `.env`):

```sh
IMAGE_TAG=edge docker compose up -d
```

`latest` only exists once a `v*` tag has been released — until then, use `IMAGE_TAG=edge`.

The package starts out private on GHCR. Either make it public in the package settings, or run `docker login ghcr.io` on the server first.

### Build locally instead

To build from source instead of pulling from GHCR, add a `build: .` line under `bot:` in `compose.yml` (or an override file), then run `docker compose up -d --build`.

### Build the image alone

```sh
docker build -t image-converter-bot .
docker build --target test .      # runs go vet + the full test suite against real libvips
```

## 4. Use it

### Set up a server (needs **Manage Server**)

```text
/config channel add channel:#vrchat-images
/config dimensions width:1500 height:1500
```

| Command | Effect |
|---|---|
| `/config show` | Show the current settings |
| `/config channel add channel:#ch` | Enable automatic conversion in a channel |
| `/config channel remove channel:#ch` | Disable it |
| `/config channel list` | List the enabled channels |
| `/config dimensions width:<1–16384> height:<1–16384>` | Maximum output size (default 1500 × 1500) |
| `/config max-file-size enabled:true size-mib:4` | Limit the output file size |
| `/config max-file-size enabled:false` | Remove the limit (the default) |
| `/config jpeg-quality quality:<1–100>` | JPEG quality (default 90) |
| `/config preserve-alpha enabled:<true/false>` | Keep transparency as PNG (default on) |
| `/config strip-metadata enabled:<true/false>` | Remove EXIF/GPS metadata (default on) |
| `/config delete-original enabled:<true/false>` | Delete the user's original message after conversion (default off); when on, the result is posted as a standalone message instead of a reply |
| `/config reset` | Restore the defaults and clear the channel list (asks for confirmation) |

Every `/config` reply is visible only to you. A new server starts with no enabled channels, so the bot does nothing until an admin runs `/config channel add`.

### Convert without an enabled channel

```text
/convert image:<attach a file>
```

Anyone can use `/convert`. It uses the server's settings and returns the same result message as automatic mode.

### Messages with several images

Each image gets its own reply.

## Discord CDN URLs expire

Discord attachment links are signed and expire, usually after about 24 hours. The bot reads the expiry from the URL's `ex` parameter and shows it as a live relative time.

When a link has expired, or is about to, press **Refresh URL**. The bot fetches the same message again, which gets Discord to issue a freshly signed link. It then edits that message with the new URL and expiry. Nothing is re-encoded or re-uploaded, and no new message is posted.

The button stores no state, so buttons on old messages keep working after the bot restarts. The image itself lives only in Discord. If the result message is deleted, the image is gone. This bot isn't permanent image hosting.

## HEIC support

HEIC decoding needs libvips built with libheif, plus an HEVC decoder plugin. The Docker image is based on Debian trixie and installs `libvips42t64`, `libheif1` and `libheif-plugin-libde265`.

The build doesn't take it on trust that these packages work. It runs `bot -selfcheck` inside the final image, which decodes an embedded HEVC-coded HEIC file and round-trips JPEG, PNG and WebP. If that fails, the image build fails. The bot runs the same check on every startup and refuses to start if HEIC can't be decoded.

If you run the bot outside Docker, you need libvips ≥ 8.10 with HEIF support and an HEVC decoder. On Debian/Ubuntu: `apt install libvips-dev libheif-plugin-libde265`. Build with `CGO_ENABLED=1`.

## Development

```sh
go test ./...            # needs libvips locally; otherwise use: docker build --target test .
```

CI (`.github/workflows/ci.yml`) runs on every push and pull request. The test and lint jobs run inside `golang:1.26-trixie` with libvips installed, because govips needs CGO even to vet or lint:

- **test:** checks gofmt and `go mod tidy`, then runs `go vet` and `go test -race`.
- **lint:** runs `golangci-lint` v2.12.2 with the rules in `.golangci.yml`.

After both pass on `main` or on a `v*` tag, CI builds the image and pushes it to GHCR. That build includes the HEIC self-check.

- The test fixtures in `internal/imageproc/testdata` are synthetic. Regenerate them with `tools/genfixtures/generate.sh`; its header shows the one-line `docker run` command.
- The layout follows `SPEC.md`:
  - `internal/imageproc`: the libvips pipeline
  - `internal/bot`: Discord handlers
  - `internal/config`: per-guild settings and cache
  - `internal/database`: SQLite and migrations
  - `internal/discordurl`: URL expiry and the result message format
  - `internal/worker`: the bounded worker pool
