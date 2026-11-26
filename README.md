# mcp-jetkvm

[![Test](https://github.com/conallob/mcp-jetkvm/actions/workflows/test.yml/badge.svg)](https://github.com/conallob/mcp-jetkvm/actions/workflows/test.yml)
[![Lint](https://github.com/conallob/mcp-jetkvm/actions/workflows/lint.yml/badge.svg)](https://github.com/conallob/mcp-jetkvm/actions/workflows/lint.yml)
[![codecov](https://codecov.io/gh/conallob/mcp-jetkvm/branch/main/graph/badge.svg)](https://codecov.io/gh/conallob/mcp-jetkvm)
[![Go Report Card](https://goreportcard.com/badge/github.com/conallob/mcp-jetkvm)](https://goreportcard.com/report/github.com/conallob/mcp-jetkvm)
[![Latest release](https://img.shields.io/github/v/release/conallob/mcp-jetkvm)](https://github.com/conallob/mcp-jetkvm/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/conallob/mcp-jetkvm)](go.mod)
[![License](https://img.shields.io/github/license/conallob/mcp-jetkvm)](LICENSE)

A local [MCP](https://modelcontextprotocol.io) server to interact with a [JetKVM](https://jetkvm.com) device, including capturing screenshots and video from the connected machine's display, and mounting/configuring virtual USB boot devices.

## Tools

| Tool | Description |
|------|-------------|
| `list_devices` | List the configured JetKVM devices (ID and host, never passwords). |
| `take_screenshot` | Capture a PNG screenshot from the JetKVM device. Returns the image inline or saves it to a file. |
| `record_video` | Record an MP4 video clip from the JetKVM device for 1–60 seconds. |
| `list_virtual_media_files` | List image files (ISO/IMG/QCOW2/WDI/VMDK) stored on the device, plus remaining storage space. |
| `get_virtual_media_state` | Get the image currently mounted as a virtual USB boot device, if any. |
| `check_virtual_media_url` | Check whether a remote HTTP(S) URL can be mounted directly without downloading it to the device first. |
| `mount_virtual_media` | Mount an image (from device storage or an HTTP(S) URL) as a virtual USB CD/DVD or disk drive so the target host can boot from it. |
| `unmount_virtual_media` | Unmount the currently mounted virtual USB boot device. |
| `delete_virtual_media_file` | Delete an image file from the device's local storage. |
| `get_usb_devices` | Get which virtual USB device classes (mice, keyboard, mass storage, serial console, audio) are presented to the host. |
| `set_usb_devices` | Configure the full set of virtual USB device classes presented to the host. |
| `set_usb_device_state` | Enable or disable a single virtual USB device class without changing the others. |
| `get_usb_emulation_state` | Check whether the device's USB controller is currently bound to the host. |
| `set_usb_emulation_state` | Bind or unbind the device's USB controller, turning all USB emulation on or off. |

## Requirements

- A JetKVM device on the local network
- [ffmpeg](https://ffmpeg.org) in your `PATH` (used to decode H.264 video frames)

## Installation

### Homebrew (macOS / Linux)

```sh
brew tap conallob/tap
brew install mcp-jetkvm
```

This automatically installs `ffmpeg` as a dependency.

### From source

```sh
go install github.com/conallob/mcp-jetkvm/cmd/mcp-jetkvm@latest
```

## Configuration

Set these environment variables before starting the server:

| Variable | Required | Description |
|----------|----------|-------------|
| `JETKVM_HOST` | Yes | IP address or hostname of the JetKVM device (e.g. `192.168.1.100`) |
| `JETKVM_PASSWORD` | No | Device password (leave empty if the device is in no-password mode) |

### Several JetKVM devices

By default one server talks to one JetKVM. To have one server operate across several, list their IDs in `JETKVM_DEVICES` and give each its own host and password:

| Variable | Required | Description |
|----------|----------|-------------|
| `JETKVM_DEVICES` | No | Comma-separated device IDs, e.g. `homelab-ctrl-1,homelab-ctrl-2`. When set, `JETKVM_HOST` and `JETKVM_PASSWORD` are ignored. |
| `JETKVM_<ID>_HOST` | Yes, per device | Host of that device. `<ID>` is the ID upper-cased with every character other than `A-Z` and `0-9` replaced by `_` (`homelab-ctrl-1` becomes `JETKVM_HOMELAB_CTRL_1_HOST`). |
| `JETKVM_<ID>_PASSWORD` | No | Password of that device. |

Every tool then takes an optional `kvm` argument naming the device to act on. It is required when more than one device is configured, and optional when there is only one. Use `list_devices` to see the IDs.

```json
{
  "mcpServers": {
    "jetkvm": {
      "command": "mcp-jetkvm",
      "env": {
        "JETKVM_DEVICES": "ctrl-1,ctrl-2",
        "JETKVM_CTRL_1_HOST": "192.168.1.101",
        "JETKVM_CTRL_1_PASSWORD": "password-1",
        "JETKVM_CTRL_2_HOST": "192.168.1.102",
        "JETKVM_CTRL_2_PASSWORD": "password-2"
      }
    }
  }
}
```

## Usage with Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS):

```json
{
  "mcpServers": {
    "jetkvm": {
      "command": "mcp-jetkvm",
      "env": {
        "JETKVM_HOST": "192.168.1.100",
        "JETKVM_PASSWORD": "your-password"
      }
    }
  }
}
```

## HTTP transport

By default the server speaks MCP over stdio. `--http <addr>` serves MCP over HTTP (the streamable HTTP transport) at `/mcp` instead, so one server can be shared by several clients or machines.

```sh
mcp-jetkvm --http 127.0.0.1:8080
```

This server can send keystrokes to the connected machines and boot them from mounted images, so authentication is not optional off the local machine. `--auth` selects it:

| `--auth` | Behaviour |
|----------|-----------|
| `none` (default) | No authentication. Refused unless the listen address is loopback (`127.0.0.1`, `::1`, `localhost`). |
| `bearer` | Requests must carry `Authorization: Bearer <token>`. The token is read from `JETKVM_MCP_BEARER_TOKEN` (at least 16 characters, so it never appears in `ps`). |
| `oauth2` | Requests must carry an OAuth 2.0 access token from an external authorization server. The server is a resource server: it validates tokens by [introspection (RFC 7662)](https://www.rfc-editor.org/rfc/rfc7662) and publishes [protected resource metadata (RFC 9728)](https://www.rfc-editor.org/rfc/rfc9728) so MCP clients can discover where to get a token. |

OAuth 2.0 flags (all but the last two are required; the client secret is read from `JETKVM_MCP_OAUTH_CLIENT_SECRET`):

| Flag | Description |
|------|-------------|
| `--oauth-introspection-url` | The authorization server's introspection endpoint. |
| `--oauth-issuer` | Authorization server URL, advertised to clients. |
| `--oauth-resource` | This server's public MCP URL, e.g. `https://kvm.example/mcp`. A token that names an audience must name this. |
| `--oauth-client-id` | Client ID this server uses to call the introspection endpoint. |
| `--oauth-scope` | Space-separated scopes every token must have. |

```sh
export JETKVM_MCP_OAUTH_CLIENT_SECRET=...
mcp-jetkvm --http :8080 --auth oauth2 \
  --oauth-introspection-url https://idp.example/oauth2/introspect \
  --oauth-issuer https://idp.example \
  --oauth-resource https://kvm.example/mcp \
  --oauth-client-id mcp-jetkvm --oauth-scope kvm
```

The server does not terminate TLS. Put it behind a TLS-terminating proxy (or a private network such as Tailscale) when using `bearer` or `oauth2` off the local machine, since tokens are sent in the clear otherwise. Each request is introspected, with no caching. If the introspection endpoint cannot be reached, requests fail with 500 (the cause is logged, not returned).

With `--auth none`, requests that arrive on a loopback address with a non-local `Host` header are rejected, to stop DNS rebinding. That check is off when `bearer` or `oauth2` is on, so a reverse proxy on the same machine can forward for its public name.

## How it works

JetKVM streams the connected machine's display exclusively over WebRTC (H.264), and exposes device configuration (including virtual media and USB gadget settings) via JSON-RPC 2.0 over a WebRTC data channel. This server:

1. Authenticates with the device (`POST /auth/login-local`)
2. Establishes a WebRTC session via the legacy SDP exchange endpoint (`POST /webrtc/session`)
3. For **screenshots/video**: receives the H.264 RTP video stream via [pion/webrtc](https://github.com/pion/webrtc)
   - **screenshots**: captures the first IDR keyframe and decodes it to PNG via `ffmpeg`
   - **video**: pipes H.264 NAL units into `ffmpeg` and muxes them into a fragmented MP4
4. For **virtual media / USB device tools**: opens the `rpc` data channel and issues JSON-RPC 2.0 requests (e.g. `mountWithStorage`, `getUsbDevices`), matching responses by request ID

Each tool call opens its own short-lived WebRTC session against the device and tears it down afterward.

## Releases

Tagged releases are built automatically via GitHub Actions and published to GitHub Releases and the [homebrew-tap](https://github.com/conallob/homebrew-tap).

Pull requests that touch the release config build a snapshot (no publishing) so a broken `.goreleaser.yaml` is caught before tagging. To try it locally: `goreleaser release --snapshot --clean --skip=publish`. `mcp-jetkvm --version` prints the release version (`dev` for a plain `go build`).

To release a new version:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The release workflow mints a short-lived token for `conallob/homebrew-tap` from a GitHub App installed on that repository, so there is no personal access token to expire. The repository needs the App's ID as the Actions variable `HOMEBREW_TAP_APP_ID` and its private key as the secret `HOMEBREW_TAP_APP_PRIVATE_KEY`.

## License

BSD 3-Clause — see [LICENSE](LICENSE).
