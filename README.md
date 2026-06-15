# mcp-jetkvm

[![codecov](https://codecov.io/gh/conallob/mcp-jetkvm/branch/main/graph/badge.svg)](https://codecov.io/gh/conallob/mcp-jetkvm)

A local [MCP](https://modelcontextprotocol.io) server to interact with a [JetKVM](https://jetkvm.com) device, including capturing screenshots and video from the connected machine's display.

## Tools

| Tool | Description |
|------|-------------|
| `take_screenshot` | Capture a PNG screenshot from the JetKVM device. Returns the image inline or saves it to a file. |
| `record_video` | Record an MP4 video clip from the JetKVM device for 1–60 seconds. |

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

## How it works

JetKVM streams the connected machine's display exclusively over WebRTC (H.264). This server:

1. Authenticates with the device (`POST /auth/login-local`)
2. Establishes a WebRTC session via the legacy SDP exchange endpoint (`POST /webrtc/session`)
3. Receives the H.264 RTP video stream via [pion/webrtc](https://github.com/pion/webrtc)
4. For **screenshots**: captures the first IDR keyframe and decodes it to PNG via `ffmpeg`
5. For **video**: pipes H.264 NAL units into `ffmpeg` and muxes them into a fragmented MP4

## Releases

Tagged releases are built automatically via GitHub Actions and published to GitHub Releases and the [homebrew-tap](https://github.com/conallob/homebrew-tap).

To release a new version:

```sh
git tag v0.1.0
git push origin v0.1.0
```

Add a `HOMEBREW_TAP_GITHUB_TOKEN` secret to the repository with a GitHub personal access token that has write access to `conallob/homebrew-tap`.

## License

BSD 3-Clause — see [LICENSE](LICENSE).
