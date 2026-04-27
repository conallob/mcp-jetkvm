package jetkvm

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RunServer starts the MCP stdio server.
func RunServer() error {
	s := server.NewMCPServer("mcp-jetkvm", "0.1.0",
		server.WithToolCapabilities(false),
	)

	s.AddTool(
		mcp.NewTool("take_screenshot",
			mcp.WithDescription("Capture a screenshot from the JetKVM device. Returns a PNG image of the connected machine's display."),
			mcp.WithString("output_path",
				mcp.Description("Optional file path to save the PNG (e.g. /tmp/screen.png). If omitted the image is returned inline."),
			),
		),
		handleTakeScreenshot,
	)

	s.AddTool(
		mcp.NewTool("record_video",
			mcp.WithDescription("Record a video clip from the JetKVM device and save it as an MP4 file."),
			mcp.WithString("output_path",
				mcp.Description("File path where the MP4 will be saved (e.g. /tmp/capture.mp4)."),
				mcp.Required(),
			),
			mcp.WithNumber("duration",
				mcp.Description("Recording duration in seconds (1–60, default 10)."),
			),
		),
		handleRecordVideo,
	)

	return server.ServeStdio(s)
}

func makeClient(ctx context.Context) (*Client, error) {
	host := os.Getenv("JETKVM_HOST")
	if host == "" {
		return nil, fmt.Errorf("JETKVM_HOST environment variable is required")
	}
	return NewClient(ctx, host, os.Getenv("JETKVM_PASSWORD"))
}

func requireFFmpeg() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH — install ffmpeg to use this tool")
	}
	return nil
}

func handleTakeScreenshot(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	client, err := makeClient(ctx)
	if err != nil {
		return nil, err
	}

	png, err := TakeScreenshot(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("capture screenshot: %w", err)
	}

	outputPath, _ := req.Params.Arguments["output_path"].(string)
	if outputPath != "" {
		if err := os.WriteFile(outputPath, png, 0o644); err != nil {
			return nil, fmt.Errorf("writing PNG: %w", err)
		}
		return mcp.NewToolResultText("Screenshot saved to " + outputPath), nil
	}

	return mcp.NewToolResultImage(
		"Screenshot from JetKVM device",
		base64.StdEncoding.EncodeToString(png),
		"image/png",
	), nil
}

func handleRecordVideo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, err
	}

	outputPath, _ := req.Params.Arguments["output_path"].(string)
	if outputPath == "" {
		return nil, fmt.Errorf("output_path is required")
	}

	durationSecs := 10.0
	if d, ok := req.Params.Arguments["duration"].(float64); ok && d > 0 {
		durationSecs = d
	}
	if durationSecs < 1 {
		durationSecs = 1
	} else if durationSecs > 60 {
		durationSecs = 60
	}
	duration := time.Duration(durationSecs) * time.Second

	ctx, cancel := context.WithTimeout(ctx, duration+30*time.Second)
	defer cancel()

	client, err := makeClient(ctx)
	if err != nil {
		return nil, err
	}

	if err := RecordVideo(ctx, client, outputPath, duration); err != nil {
		return nil, fmt.Errorf("record video: %w", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Video recorded to %s (%.0fs)", outputPath, durationSecs)), nil
}
