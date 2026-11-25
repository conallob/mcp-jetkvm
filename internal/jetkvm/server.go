package jetkvm

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RunServer starts the MCP stdio server.
func RunServer() error {
	s := server.NewMCPServer("mcp-jetkvm", "0.1.0",
		server.WithToolCapabilities(false),
	)

	addTool(s,
		mcp.NewTool("list_devices",
			mcp.WithDescription("List the configured JetKVM devices (ID and host). Pass an ID as the \"kvm\" argument of any other tool to choose which JetKVM it acts on."),
		),
		handleListDevices,
	)

	addTool(s,
		mcp.NewTool("take_screenshot",
			mcp.WithDescription("Capture a screenshot from the JetKVM device. Returns a PNG image of the connected machine's display."),
			mcp.WithString("output_path",
				mcp.Description("Optional file path to save the PNG (e.g. /tmp/screen.png). If omitted the image is returned inline."),
			),
		),
		handleTakeScreenshot,
	)

	addTool(s,
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

	addTool(s,
		mcp.NewTool("list_virtual_media_files",
			mcp.WithDescription("List image files (ISO/IMG/QCOW2/WDI/VMDK) stored on the JetKVM device and its remaining storage space. These can be mounted as a virtual USB boot device."),
		),
		handleListVirtualMediaFiles,
	)

	addTool(s,
		mcp.NewTool("get_virtual_media_state",
			mcp.WithDescription("Get the image currently mounted as a virtual USB boot device on the JetKVM, if any."),
		),
		handleGetVirtualMediaState,
	)

	addTool(s,
		mcp.NewTool("check_virtual_media_url",
			mcp.WithDescription("Check whether a remote HTTP(S) URL can be mounted directly as virtual media without first downloading it to the device's storage."),
			mcp.WithString("url",
				mcp.Description("URL of the image to check (e.g. https://example.com/installer.iso)."),
				mcp.Required(),
			),
		),
		handleCheckVirtualMediaURL,
	)

	addTool(s,
		mcp.NewTool("mount_virtual_media",
			mcp.WithDescription("Mount an image as a virtual USB boot device on the JetKVM, so the connected host sees it as a CD/DVD or disk drive during boot (e.g. for OS installation). Source is either a file already on device storage, or a remote HTTP(S) URL streamed directly."),
			mcp.WithString("source",
				mcp.Description("Where the image comes from."),
				mcp.Enum("storage", "http"),
				mcp.Required(),
			),
			mcp.WithString("filename",
				mcp.Description("Filename of an image already stored on the device (required when source is \"storage\")."),
			),
			mcp.WithString("url",
				mcp.Description("HTTP(S) URL of the image to mount (required when source is \"http\")."),
			),
			mcp.WithString("mode",
				mcp.Description("How the image is presented to the host's USB controller."),
				mcp.Enum("cdrom", "disk"),
				mcp.DefaultString("cdrom"),
			),
		),
		handleMountVirtualMedia,
	)

	addTool(s,
		mcp.NewTool("unmount_virtual_media",
			mcp.WithDescription("Unmount the currently mounted virtual USB boot device from the JetKVM."),
		),
		handleUnmountVirtualMedia,
	)

	addTool(s,
		mcp.NewTool("delete_virtual_media_file",
			mcp.WithDescription("Delete an image file from the JetKVM device's local storage."),
			mcp.WithString("filename",
				mcp.Description("Filename of the image to delete."),
				mcp.Required(),
			),
		),
		handleDeleteVirtualMediaFile,
	)

	addTool(s,
		mcp.NewTool("get_usb_devices",
			mcp.WithDescription("Get which virtual USB device classes (keyboard, mice, mass storage, serial console, audio) the JetKVM currently presents to the connected host."),
		),
		handleGetUSBDevices,
	)

	addTool(s,
		mcp.NewTool("set_usb_devices",
			mcp.WithDescription("Configure the full set of virtual USB device classes the JetKVM presents to the connected host. Disabling mass_storage hides any mounted virtual media from the host even if an image is mounted."),
			mcp.WithBoolean("absolute_mouse", mcp.Required()),
			mcp.WithBoolean("relative_mouse", mcp.Required()),
			mcp.WithBoolean("keyboard", mcp.Required()),
			mcp.WithBoolean("mass_storage", mcp.Required()),
			mcp.WithBoolean("serial_console", mcp.Required()),
			mcp.WithBoolean("audio", mcp.Required()),
		),
		handleSetUSBDevices,
	)

	addTool(s,
		mcp.NewTool("set_usb_device_state",
			mcp.WithDescription("Enable or disable a single virtual USB device class on the JetKVM without changing the others."),
			mcp.WithString("device",
				mcp.Enum(usbDeviceNames...),
				mcp.Required(),
			),
			mcp.WithBoolean("enabled", mcp.Required()),
		),
		handleSetUSBDeviceState,
	)

	addTool(s,
		mcp.NewTool("get_usb_emulation_state",
			mcp.WithDescription("Check whether the JetKVM's USB Device Controller is bound, i.e. whether it is presenting any USB device to the connected host at all."),
		),
		handleGetUSBEmulationState,
	)

	addTool(s,
		mcp.NewTool("set_usb_emulation_state",
			mcp.WithDescription("Bind or unbind the JetKVM's USB Device Controller, turning all virtual USB device emulation to the host on or off."),
			mcp.WithBoolean("enabled", mcp.Required()),
		),
		handleSetUSBEmulationState,
	)

	return server.ServeStdio(s)
}

// addTool registers a tool, adding the optional "kvm" argument that selects
// which configured JetKVM the call is for.
func addTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	if tool.InputSchema.Properties == nil {
		tool.InputSchema.Properties = map[string]interface{}{}
	}
	tool.InputSchema.Properties["kvm"] = map[string]interface{}{
		"type":        "string",
		"description": "ID of the JetKVM to use. Required when more than one JetKVM is configured (see list_devices); optional otherwise.",
	}
	s.AddTool(tool, handler)
}

func deviceID(req mcp.CallToolRequest) string {
	id, _ := req.Params.Arguments["kvm"].(string)
	return id
}

func makeClient(ctx context.Context, id string) (*Client, error) {
	devices, err := loadDevices()
	if err != nil {
		return nil, err
	}
	device, err := pickDevice(devices, id)
	if err != nil {
		return nil, err
	}
	return NewClient(ctx, device.Host, device.Password)
}

func handleListDevices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	devices, err := loadDevices()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, d := range devices {
		fmt.Fprintf(&b, "%s: %s\n", d.ID, d.Host)
	}
	return mcp.NewToolResultText(b.String()), nil
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

	client, err := makeClient(ctx, deviceID(req))
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

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	if err := RecordVideo(ctx, client, outputPath, duration); err != nil {
		return nil, fmt.Errorf("record video: %w", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Video recorded to %s (%.0fs)", outputPath, durationSecs)), nil
}

// rpcTimeout bounds the WebRTC RPC session setup + call for the virtual
// media / USB device configuration tools below.
const rpcTimeout = 20 * time.Second

func handleListVirtualMediaFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	files, err := ListStorageFiles(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("list storage files: %w", err)
	}
	space, err := GetStorageSpace(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("get storage space: %w", err)
	}

	if len(files.Files) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf(
			"No image files stored on the device. Storage: %d bytes used, %d bytes free.",
			space.BytesUsed, space.BytesFree,
		)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Storage: %d bytes used, %d bytes free.\n", space.BytesUsed, space.BytesFree)
	for _, f := range files.Files {
		fmt.Fprintf(&b, "- %s (%d bytes, created %s)\n", f.Filename, f.Size, f.CreatedAt)
	}
	return mcp.NewToolResultText(b.String()), nil
}

func handleGetVirtualMediaState(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	state, err := GetVirtualMediaState(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("get virtual media state: %w", err)
	}

	if state.Source == "" {
		return mcp.NewToolResultText("No virtual media is currently mounted."), nil
	}

	name := state.Filename
	if state.Source == SourceHTTP {
		name = state.URL
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"Mounted: %s (source=%s, mode=%s, %d bytes)",
		name, state.Source, state.Mode, state.Size,
	)), nil
}

func handleCheckVirtualMediaURL(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	url, _ := req.Params.Arguments["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("url is required")
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	info, err := CheckMountURL(ctx, client, url)
	if err != nil {
		return nil, fmt.Errorf("check mount URL: %w", err)
	}

	if !info.Usable {
		return mcp.NewToolResultText(fmt.Sprintf("URL is not directly mountable: %s", info.Reason)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("URL is mountable directly (%d bytes).", info.Size)), nil
}

func handleMountVirtualMedia(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	source, _ := req.Params.Arguments["source"].(string)
	filename, _ := req.Params.Arguments["filename"].(string)
	url, _ := req.Params.Arguments["url"].(string)
	modeArg, _ := req.Params.Arguments["mode"].(string)
	if modeArg == "" {
		modeArg = "cdrom"
	}

	var mode VirtualMediaMode
	switch modeArg {
	case "cdrom":
		mode = ModeCDROM
	case "disk":
		mode = ModeDisk
	default:
		return nil, fmt.Errorf("mode must be \"cdrom\" or \"disk\", got %q", modeArg)
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	switch source {
	case "storage":
		if filename == "" {
			return nil, fmt.Errorf("filename is required when source is \"storage\"")
		}
		if err := MountWithStorage(ctx, client, filename, mode); err != nil {
			return nil, fmt.Errorf("mount virtual media: %w", err)
		}
		return mcp.NewToolResultText(fmt.Sprintf("Mounted %s from storage as %s.", filename, modeArg)), nil
	case "http":
		if url == "" {
			return nil, fmt.Errorf("url is required when source is \"http\"")
		}
		if err := MountWithHTTP(ctx, client, url, mode); err != nil {
			return nil, fmt.Errorf("mount virtual media: %w", err)
		}
		return mcp.NewToolResultText(fmt.Sprintf("Mounted %s over HTTP as %s.", url, modeArg)), nil
	default:
		return nil, fmt.Errorf("source must be \"storage\" or \"http\", got %q", source)
	}
}

func handleUnmountVirtualMedia(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	if err := UnmountImage(ctx, client); err != nil {
		return nil, fmt.Errorf("unmount virtual media: %w", err)
	}
	return mcp.NewToolResultText("Virtual media unmounted."), nil
}

func handleDeleteVirtualMediaFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filename, _ := req.Params.Arguments["filename"].(string)
	if filename == "" {
		return nil, fmt.Errorf("filename is required")
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	if err := DeleteStorageFile(ctx, client, filename); err != nil {
		return nil, fmt.Errorf("delete storage file: %w", err)
	}
	return mcp.NewToolResultText(fmt.Sprintf("Deleted %s from device storage.", filename)), nil
}

func handleGetUSBDevices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	devices, err := GetUSBDevices(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("get USB devices: %w", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"absolute_mouse=%t relative_mouse=%t keyboard=%t mass_storage=%t serial_console=%t audio=%t",
		devices.AbsoluteMouse, devices.RelativeMouse, devices.Keyboard,
		devices.MassStorage, devices.SerialConsole, devices.Audio,
	)), nil
}

func handleSetUSBDevices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	devices := USBDevices{}
	for name, dst := range map[string]*bool{
		"absolute_mouse": &devices.AbsoluteMouse,
		"relative_mouse": &devices.RelativeMouse,
		"keyboard":       &devices.Keyboard,
		"mass_storage":   &devices.MassStorage,
		"serial_console": &devices.SerialConsole,
		"audio":          &devices.Audio,
	} {
		v, ok := req.Params.Arguments[name].(bool)
		if !ok {
			return nil, fmt.Errorf("%s is required", name)
		}
		*dst = v
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	if err := SetUSBDevices(ctx, client, devices); err != nil {
		return nil, fmt.Errorf("set USB devices: %w", err)
	}
	return mcp.NewToolResultText("USB devices updated."), nil
}

func handleSetUSBDeviceState(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	device, _ := req.Params.Arguments["device"].(string)
	if device == "" {
		return nil, fmt.Errorf("device is required")
	}
	enabled, ok := req.Params.Arguments["enabled"].(bool)
	if !ok {
		return nil, fmt.Errorf("enabled is required")
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	if err := SetUSBDeviceState(ctx, client, device, enabled); err != nil {
		return nil, fmt.Errorf("set USB device state: %w", err)
	}
	return mcp.NewToolResultText(fmt.Sprintf("%s %s.", device, map[bool]string{true: "enabled", false: "disabled"}[enabled])), nil
}

func handleGetUSBEmulationState(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	enabled, err := GetUSBEmulationState(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("get USB emulation state: %w", err)
	}
	return mcp.NewToolResultText(fmt.Sprintf("USB emulation enabled: %t", enabled)), nil
}

func handleSetUSBEmulationState(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	enabled, ok := req.Params.Arguments["enabled"].(bool)
	if !ok {
		return nil, fmt.Errorf("enabled is required")
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, deviceID(req))
	if err != nil {
		return nil, err
	}

	if err := SetUSBEmulationState(ctx, client, enabled); err != nil {
		return nil, fmt.Errorf("set USB emulation state: %w", err)
	}
	return mcp.NewToolResultText(fmt.Sprintf("USB emulation %s.", map[bool]string{true: "enabled", false: "disabled"}[enabled])), nil
}
