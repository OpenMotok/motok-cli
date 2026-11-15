# Motok.ai CLI

An independent Go project for calling official model APIs and compatible gateways.
No backend process, database or Node.js is required.
Inspired by [flatkey-cli](https://github.com/flatkey-ai/flatkey-cli).

[简体中文](README.zh-CN.md)

## Install

Install the latest release on macOS or Linux (no Go, no sudo; installs to `~/.local/bin`):

```sh
curl -fsSL https://raw.githubusercontent.com/OpenMotok/motok-cli/main/install.sh | sh
```

Pick a version or destination:

```sh
curl -fsSL https://raw.githubusercontent.com/OpenMotok/motok-cli/main/install.sh | sh -s -- --version v0.1.0 --bin-dir ~/bin
```

The installer downloads the matching archive from
[GitHub Releases](https://github.com/OpenMotok/motok-cli/releases) over HTTPS and
verifies it against `SHA256SUMS`; a failed check never replaces an installed binary.
`MOTOK_VERSION` and `MOTOK_BIN_DIR` adjust defaults. `MOTOK_RELEASE_BASE_URL`
points to a self-hosted mirror directory (requires `--version`). If the destination
is not on PATH, the installer prints how to add it.

Windows users download the matching ZIP from Releases, verify it against
`SHA256SUMS`, and add the directory containing `motok.exe` to PATH.

After installing:

```sh
export MOTOK_API_KEY=your-key   # add to ~/.zshrc or ~/.bashrc
motok init
motok login
```

## Development and releases

Development requires Go 1.26.1 or later:

```sh
go build -trimpath -o bin/motok ./cmd/motok
./bin/motok help
```

Build all release archives and install one locally:

```sh
sh scripts/build-release.sh v0.1.0
sh install.sh --from-dir ./dist --version v0.1.0
motok --version
```

To publish a release, push a `v*` tag. GitHub Actions runs the tests, builds macOS,
Linux and Windows archives for AMD64 and ARM64, and publishes them with
`SHA256SUMS` and `install.sh` as a GitHub Release.

```sh
git tag v0.1.0 && git push origin v0.1.0
```

## Channels and capability routes

API keys and URLs come **only** from configuration and environment variables.
There are no profiles, `--profile`, `--api-key` or `--base-url` arguments.

```sh
motok init
motok config path
motok config show
```

`motok init` (also `motok config init`) creates a private YAML file (0600) without overwriting existing files. It contains one Motok channel, an empty `api_key`, and text/image/video routes. Fill in your API URL, key and model IDs before generating. `MOTOK_BASE_URL`, when present at initialization, supplies the initial URL; otherwise the file uses an explicit host placeholder.
YAML supports comments and is the default. JSON remains supported through `.json`
files; a [JSON example](config.example.json) is also provided. Unknown fields,
duplicate YAML keys and multiple documents are rejected. `version` defaults to 1
when omitted.

The default location is the OS user configuration directory under
`motok/config.yaml`. Automatic discovery checks `config.yaml`, then `config.yml`,
then the existing `config.json` for compatibility. Explicit `MOTOK_CONFIG` takes
precedence. Select another file with:

```sh
export MOTOK_CONFIG=./motok.config.yaml
motok init
```

Configure named **channels** with their protocol and credentials, then map each
capability to a channel and model. See [config.example.yaml](config.example.yaml):

```yaml
version: 1
language: en

# Channel names are arbitrary; protocol selects the request protocol.
channels:
  claude:
    protocol: anthropic
    base_url: https://api.anthropic.com/v1
    api_key_env: ANTHROPIC_API_KEY
  openai:
    protocol: openai
    base_url: https://api.openai.com/v1
    api_key_env: OPENAI_API_KEY
    max_tokens_field: max_completion_tokens
  media:
    protocol: openai
    base_url: http://127.0.0.1:8092/v1
    api_key_env: MOTOK_API_KEY
    base_url_env: MOTOK_BASE_URL

# routes.channel references a channel name.
routes:
  text:
    channel: claude
    model: your-text-model
  image:
    channel: openai
    model: your-image-model
  video:
    channel: media
    model: your-video-model
```

Replace the example model placeholders with real model IDs available to your
account. `text generate` automatically uses `routes.text`; image generation and
editing use `routes.image`; all video commands use `routes.video`. `response
generate` also uses `routes.text` and requires an OpenAI-compatible channel.
A single channel can serve multiple capabilities with different route models.
No whole-configuration switching is needed. `channels.<name>.protocol` selects the
wire protocol (`openai`, `anthropic`, `gemini`; omitted means `openai`).
`routes.<capability>.channel` references a custom channel name. These fields have
different roles. For example, channel `company-proxy` may use protocol `anthropic`,
while `routes.text.channel` is `company-proxy`.

`models`, `login`, `status`, `auth status` and native `request` commands default to
the text route. Use `--type image` or `--type video` to operate on that capability's
channel. Model discovery lists the selected channel's models; it does not infer
which models support a modality. Generation commands reject a conflicting `--type`.

```sh
motok models --type image --json
motok auth status --type video
motok text generate --prompt "Hello"
motok image generate --prompt "A watercolor cat" -o cat.png
```

Set each channel's key privately through its referenced environment variable, or
put `api_key` directly in a private configuration file. Strings do not expand shell
variables, and `.env` files are not loaded automatically. `config show` redacts all
channel keys, including keys of unused channels.

Precedence:

1. Channel and protocol: the capability's `routes` entry selects a `channels` entry.
2. API key: nonempty `api_key_env` value, then channel `api_key`.
3. URL: nonempty `base_url_env` value, then channel `base_url`, then protocol default.
4. Model: `--model`, then `MOTOK_TEXT_MODEL` / `MOTOK_IMAGE_MODEL` /
   `MOTOK_VIDEO_MODEL`, then route `model`, then channel `model`.
5. Language: `--lang`, then `MOTOK_LANG`, then configuration, then English.

Omitted environment-variable references use the selected protocol's standard
variables: `OPENAI_API_KEY` / `OPENAI_BASE_URL`, `ANTHROPIC_API_KEY` /
`ANTHROPIC_BASE_URL`, or `GEMINI_API_KEY` / `GEMINI_BASE_URL`. To isolate multiple
channels using the same protocol, give each a distinct `api_key_env` and
`base_url_env`. `MOTOK_API_KEY` and `MOTOK_BASE_URL` only apply to channels
explicitly referencing those variables. Credentials never fall back across protocols.

Without a configuration file, all three routes use a default OpenAI channel and
its standard environment variables. Run `config init` to configure multiple channels.
Old profile-based configurations are no longer accepted; edit them into the
channels/routes format. Existing configuration files are never auto-replaced.

Custom URL path prefixes are preserved. URLs without a path receive `/v1` for
OpenAI/Anthropic or `/v1beta` for Gemini. HTTP is supported for self-hosted services;
use HTTPS across a network. API redirects are rejected to avoid forwarding keys.
`max_tokens_field` on a channel selects `max_completion_tokens` (default) or
`max_tokens` for compatible text APIs. `--max-tokens` defaults to 1024; Anthropic
and Gemini use their native fields.

## Commands

```sh
motok auth status
motok login
motok models --json
motok text generate --prompt "Hello"
motok text generate --model your-model --prompt "Hello"
motok response generate --prompt "Hello" --json
motok image generate --prompt "A watercolor cat" -o cat.png
motok image edit --prompt "Add a blue background" --file cat.png -o edited.png
motok video generate --model your-video-model --prompt "A cat walking" --wait -o cat.mp4
motok video status TASK_ID
motok video download TASK_ID -o cat.mp4
motok help --lang zh
motok help --ai
```

`login` validates the configured key using the model-list endpoint; it does not
open a browser, create a user account or store another session. Services without
a model-list endpoint can still be used for generation; their `login`/`models`
commands will fail. `auth status` is local configuration status, not a network
health check. `status` performs the same key validation as `login`.

| Capability | OpenAI-compatible | Anthropic | Gemini |
| --- | --- | --- | --- |
| Text and model discovery | Yes | Yes | Yes |
| Responses API | Yes, if service supports it | No | No |
| Image generation/editing | Yes, if model supports it | No | Image-capable generateContent models |
| Video jobs and download | Compatible services implementing `/videos` | No | No |
| Native JSON requests | Yes | Yes | Yes |

Video commands target the compatible `/videos` contract already exposed by
Motok.ai. They are not adapters for Ark, Veo or other native video job schemas.
The official OpenAI Videos API was shut down on September 24, 2026, so video
helpers reject `api.openai.com`; use a video-capable compatible service instead.
See the [official API reference](https://developers.openai.com/api/reference/resources/videos/methods/create).
Availability, model IDs and media options always depend on the configured service.
There is no fabricated credit balance, audio endpoint, upload service or browser
authorization flow.

## Scripts and agents

```sh
motok image generate --prompt "A watercolor cat" --dry-run --json
motok text generate --prompt - --json
motok request POST /embeddings --data request.json --json
motok request POST /messages --type text --data message.json --json
motok request POST /audio/speech --data speech.json -o speech.mp3
```

`request` uses an endpoint relative to the configured API prefix, its native
JSON payload and the selected protocol's auth headers. Use it for provider
options or endpoints not covered by generation helpers. It does not translate
native schemas. `--data -` and `--prompt -` read stdin. JSON request input is capped
at 4 MiB. Helpers buffer JSON responses up to 32 MiB; binary downloads stream.
Normal text helpers are non-streaming; use native requests for additional options.

`--json` emits JSON on stdout; errors are JSON on stderr with exit code 1. Human
progress messages go to stderr and are disabled in JSON mode. `--dry-run` makes
no network requests and needs no key. It prints the planned request, without auth
headers or reference image bytes. Generated content goes into a new private file
with `--output`/`-o`; existing files are never overwritten. `--json` with `-o`
returns file metadata. For image responses containing multiple outputs, the helper
saves the first image; use `--json` without `-o` for the complete native response.
Media URL downloads never receive API credentials and redirects are not followed.

Video polling defaults to 10 seconds, with an overall timeout of 10 minutes.
Adjust with `--poll-interval` and `--timeout`. The task ID is printed to stderr in
human mode before waiting. A timeout does not cancel a remote generation job.
Do not blindly retry generation requests: they can create additional billable
jobs. The CLI never automatically retries generation POST requests.

## Development

```sh
go test -race ./...
go vet ./...
go build -o bin/motok ./cmd/motok
sh -n install.sh scripts/build-release.sh
```

Tests use local HTTP/SMTP-free fixtures and synthetic credentials. They make no
billable calls to official services. This module is independent of `../services`
and uses a pure Go YAML parser bundled into the binary.

Protocol references: [OpenAI](https://developers.openai.com/api/reference/overview),
[Anthropic](https://platform.claude.com/docs/en/api/overview),
[Gemini](https://ai.google.dev/api/generate-content).

Channel configuration uses `protocol` only; omitted values default to `openai`.
The former `provider` field is rejected as an unknown field.

`motok config path` prints the selected absolute configuration file path without reading its contents. It also works before initialization or when the file is malformed. Add `--json` for machine-readable output.
