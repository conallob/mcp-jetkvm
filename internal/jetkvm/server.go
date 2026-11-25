package jetkvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures how the MCP server is served.
type Options struct {
	// HTTP, when non-nil, serves MCP over HTTP instead of stdio.
	HTTP *HTTPOptions
}

// RunServer starts the MCP server on stdio, or over HTTP if opts.HTTP is set.
func RunServer(opts Options) error {
	s := newMCPServer()
	if opts.HTTP != nil {
		return serveHTTP(s, *opts.HTTP)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return s.Run(ctx, &mcp.StdioTransport{})
}

// kvmArg is embedded in every tool's input. It selects which configured
// JetKVM the call is for.
type kvmArg struct {
	KVM string `json:"kvm,omitempty" jsonschema:"ID of the JetKVM to use. Required when more than one JetKVM is configured (see list_devices); optional otherwise."`
}

type kvmOnly struct{ kvmArg }

type screenshotInput struct {
	kvmArg
	OutputPath string `json:"output_path,omitempty" jsonschema:"Optional file path to save the PNG (e.g. /tmp/screen.png). If omitted the image is returned inline."`
}

type recordVideoInput struct {
	kvmArg
	OutputPath string  `json:"output_path" jsonschema:"File path where the MP4 will be saved (e.g. /tmp/capture.mp4)."`
	Duration   float64 `json:"duration,omitempty" jsonschema:"Recording duration in seconds (1-60, default 10)."`
}

type checkURLInput struct {
	kvmArg
	URL string `json:"url" jsonschema:"URL of the image to check (e.g. https://example.com/installer.iso)."`
}

type mountInput struct {
	kvmArg
	Source   string `json:"source" jsonschema:"Where the image comes from."`
	Filename string `json:"filename,omitempty" jsonschema:"Filename of an image already stored on the device (required when source is \"storage\")."`
	URL      string `json:"url,omitempty" jsonschema:"HTTP(S) URL of the image to mount (required when source is \"http\")."`
	Mode     string `json:"mode,omitempty" jsonschema:"How the image is presented to the host's USB controller."`
}

type deleteFileInput struct {
	kvmArg
	Filename string `json:"filename" jsonschema:"Filename of the image to delete."`
}

type setUSBDevicesInput struct {
	kvmArg
	AbsoluteMouse bool `json:"absolute_mouse"`
	RelativeMouse bool `json:"relative_mouse"`
	Keyboard      bool `json:"keyboard"`
	MassStorage   bool `json:"mass_storage"`
	SerialConsole bool `json:"serial_console"`
	Audio         bool `json:"audio"`
}

type setUSBDeviceStateInput struct {
	kvmArg
	Device  string `json:"device"`
	Enabled bool   `json:"enabled"`
}

type setUSBEmulationInput struct {
	kvmArg
	Enabled bool `json:"enabled"`
}

// handler is the shape of every tool in this server. Tools report failure by
// returning an error, which the SDK turns into an error result.
type handler[In any] func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error)

// addTool registers a tool whose input schema is inferred from In, with enums
// constraining the named properties to the given values.
func addTool[In any](s *mcp.Server, name, description string, enums map[string][]any, h handler[In]) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: input schema: %v", name, err))
	}
	for property, values := range enums {
		schema.Properties[property].Enum = values
	}
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: description, InputSchema: schema}, mcp.ToolHandlerFor[In, any](h))
}

func newMCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "mcp-jetkvm", Version: "0.1.0"}, nil)

	addTool(s, "list_devices", "List the configured JetKVM devices (ID and host). Pass an ID as the \"kvm\" argument of any other tool to choose which JetKVM it acts on.", nil, handleListDevices)
	addTool(s, "take_screenshot", "Capture a screenshot from the JetKVM device. Returns a PNG image of the connected machine's display.", nil, handleTakeScreenshot)
	addTool(s, "record_video", "Record a video clip from the JetKVM device and save it as an MP4 file.", nil, handleRecordVideo)
	addTool(s, "list_virtual_media_files", "List image files (ISO/IMG/QCOW2/WDI/VMDK) stored on the JetKVM device and its remaining storage space. These can be mounted as a virtual USB boot device.", nil, handleListVirtualMediaFiles)
	addTool(s, "get_virtual_media_state", "Get the image currently mounted as a virtual USB boot device on the JetKVM, if any.", nil, handleGetVirtualMediaState)
	addTool(s, "check_virtual_media_url", "Check whether a remote HTTP(S) URL can be mounted directly as virtual media without first downloading it to the device's storage.", nil, handleCheckVirtualMediaURL)
	addTool(s, "mount_virtual_media", "Mount an image as a virtual USB boot device on the JetKVM, so the connected host sees it as a CD/DVD or disk drive during boot (e.g. for OS installation). Source is either a file already on device storage, or a remote HTTP(S) URL streamed directly.", map[string][]any{
		"source": {"storage", "http"},
		"mode":   {"cdrom", "disk"},
	}, handleMountVirtualMedia)
	addTool(s, "unmount_virtual_media", "Unmount the currently mounted virtual USB boot device from the JetKVM.", nil, handleUnmountVirtualMedia)
	addTool(s, "delete_virtual_media_file", "Delete an image file from the JetKVM device's local storage.", nil, handleDeleteVirtualMediaFile)
	addTool(s, "get_usb_devices", "Get which virtual USB device classes (keyboard, mice, mass storage, serial console, audio) the JetKVM currently presents to the connected host.", nil, handleGetUSBDevices)
	addTool(s, "set_usb_devices", "Configure the full set of virtual USB device classes the JetKVM presents to the connected host. Disabling mass_storage hides any mounted virtual media from the host even if an image is mounted.", nil, handleSetUSBDevices)
	addTool(s, "set_usb_device_state", "Enable or disable a single virtual USB device class on the JetKVM without changing the others.", map[string][]any{
		"device": stringsToAny(usbDeviceNames),
	}, handleSetUSBDeviceState)
	addTool(s, "get_usb_emulation_state", "Check whether the JetKVM's USB Device Controller is bound, i.e. whether it is presenting any USB device to the connected host at all.", nil, handleGetUSBEmulationState)
	addTool(s, "set_usb_emulation_state", "Bind or unbind the JetKVM's USB Device Controller, turning all virtual USB device emulation to the host on or off.", nil, handleSetUSBEmulationState)

	return s
}

func stringsToAny(list []string) []any {
	out := make([]any, len(list))
	for i, item := range list {
		out[i] = item
	}
	return out
}

func text(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
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

func handleListDevices(_ context.Context, _ *mcp.CallToolRequest, _ kvmOnly) (*mcp.CallToolResult, any, error) {
	devices, err := loadDevices()
	if err != nil {
		return nil, nil, err
	}
	var b strings.Builder
	for _, d := range devices {
		fmt.Fprintf(&b, "%s: %s\n", d.ID, d.Host)
	}
	return text("%s", b.String()), nil, nil
}

func requireFFmpeg() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH — install ffmpeg to use this tool")
	}
	return nil
}

func handleTakeScreenshot(ctx context.Context, _ *mcp.CallToolRequest, in screenshotInput) (*mcp.CallToolResult, any, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	png, err := TakeScreenshot(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("capture screenshot: %w", err)
	}

	if in.OutputPath != "" {
		if err := os.WriteFile(in.OutputPath, png, 0o644); err != nil {
			return nil, nil, fmt.Errorf("writing PNG: %w", err)
		}
		return text("Screenshot saved to %s", in.OutputPath), nil, nil
	}

	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: "Screenshot from JetKVM device"},
		&mcp.ImageContent{Data: png, MIMEType: "image/png"},
	}}, nil, nil
}

func handleRecordVideo(ctx context.Context, _ *mcp.CallToolRequest, in recordVideoInput) (*mcp.CallToolResult, any, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, nil, err
	}
	if in.OutputPath == "" {
		return nil, nil, fmt.Errorf("output_path is required")
	}

	durationSecs := 10.0
	if in.Duration > 0 {
		durationSecs = in.Duration
	}
	if durationSecs < 1 {
		durationSecs = 1
	} else if durationSecs > 60 {
		durationSecs = 60
	}
	duration := time.Duration(durationSecs) * time.Second

	ctx, cancel := context.WithTimeout(ctx, duration+30*time.Second)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	if err := RecordVideo(ctx, client, in.OutputPath, duration); err != nil {
		return nil, nil, fmt.Errorf("record video: %w", err)
	}

	return text("Video recorded to %s (%.0fs)", in.OutputPath, durationSecs), nil, nil
}

// rpcTimeout bounds the WebRTC RPC session setup + call for the virtual
// media / USB device configuration tools below.
const rpcTimeout = 20 * time.Second

func handleListVirtualMediaFiles(ctx context.Context, _ *mcp.CallToolRequest, in kvmOnly) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	files, err := ListStorageFiles(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("list storage files: %w", err)
	}
	space, err := GetStorageSpace(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("get storage space: %w", err)
	}

	if len(files.Files) == 0 {
		return text("No image files stored on the device. Storage: %d bytes used, %d bytes free.",
			space.BytesUsed, space.BytesFree), nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Storage: %d bytes used, %d bytes free.\n", space.BytesUsed, space.BytesFree)
	for _, f := range files.Files {
		fmt.Fprintf(&b, "- %s (%d bytes, created %s)\n", f.Filename, f.Size, f.CreatedAt)
	}
	return text("%s", b.String()), nil, nil
}

func handleGetVirtualMediaState(ctx context.Context, _ *mcp.CallToolRequest, in kvmOnly) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	state, err := GetVirtualMediaState(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("get virtual media state: %w", err)
	}

	if state.Source == "" {
		return text("No virtual media is currently mounted."), nil, nil
	}

	name := state.Filename
	if state.Source == SourceHTTP {
		name = state.URL
	}
	return text("Mounted: %s (source=%s, mode=%s, %d bytes)", name, state.Source, state.Mode, state.Size), nil, nil
}

func handleCheckVirtualMediaURL(ctx context.Context, _ *mcp.CallToolRequest, in checkURLInput) (*mcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required")
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	info, err := CheckMountURL(ctx, client, in.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("check mount URL: %w", err)
	}

	if !info.Usable {
		return text("URL is not directly mountable: %s", info.Reason), nil, nil
	}
	return text("URL is mountable directly (%d bytes).", info.Size), nil, nil
}

func handleMountVirtualMedia(ctx context.Context, _ *mcp.CallToolRequest, in mountInput) (*mcp.CallToolResult, any, error) {
	modeArg := in.Mode
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
		return nil, nil, fmt.Errorf("mode must be \"cdrom\" or \"disk\", got %q", modeArg)
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	switch in.Source {
	case "storage":
		if in.Filename == "" {
			return nil, nil, fmt.Errorf("filename is required when source is \"storage\"")
		}
		if err := MountWithStorage(ctx, client, in.Filename, mode); err != nil {
			return nil, nil, fmt.Errorf("mount virtual media: %w", err)
		}
		return text("Mounted %s from storage as %s.", in.Filename, modeArg), nil, nil
	case "http":
		if in.URL == "" {
			return nil, nil, fmt.Errorf("url is required when source is \"http\"")
		}
		if err := MountWithHTTP(ctx, client, in.URL, mode); err != nil {
			return nil, nil, fmt.Errorf("mount virtual media: %w", err)
		}
		return text("Mounted %s over HTTP as %s.", in.URL, modeArg), nil, nil
	default:
		return nil, nil, fmt.Errorf("source must be \"storage\" or \"http\", got %q", in.Source)
	}
}

func handleUnmountVirtualMedia(ctx context.Context, _ *mcp.CallToolRequest, in kvmOnly) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	if err := UnmountImage(ctx, client); err != nil {
		return nil, nil, fmt.Errorf("unmount virtual media: %w", err)
	}
	return text("Virtual media unmounted."), nil, nil
}

func handleDeleteVirtualMediaFile(ctx context.Context, _ *mcp.CallToolRequest, in deleteFileInput) (*mcp.CallToolResult, any, error) {
	if in.Filename == "" {
		return nil, nil, fmt.Errorf("filename is required")
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	if err := DeleteStorageFile(ctx, client, in.Filename); err != nil {
		return nil, nil, fmt.Errorf("delete storage file: %w", err)
	}
	return text("Deleted %s from device storage.", in.Filename), nil, nil
}

func handleGetUSBDevices(ctx context.Context, _ *mcp.CallToolRequest, in kvmOnly) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	devices, err := GetUSBDevices(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("get USB devices: %w", err)
	}

	return text("absolute_mouse=%t relative_mouse=%t keyboard=%t mass_storage=%t serial_console=%t audio=%t",
		devices.AbsoluteMouse, devices.RelativeMouse, devices.Keyboard,
		devices.MassStorage, devices.SerialConsole, devices.Audio), nil, nil
}

func handleSetUSBDevices(ctx context.Context, _ *mcp.CallToolRequest, in setUSBDevicesInput) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	devices := USBDevices{
		AbsoluteMouse: in.AbsoluteMouse,
		RelativeMouse: in.RelativeMouse,
		Keyboard:      in.Keyboard,
		MassStorage:   in.MassStorage,
		SerialConsole: in.SerialConsole,
		Audio:         in.Audio,
	}
	if err := SetUSBDevices(ctx, client, devices); err != nil {
		return nil, nil, fmt.Errorf("set USB devices: %w", err)
	}
	return text("USB devices updated."), nil, nil
}

func handleSetUSBDeviceState(ctx context.Context, _ *mcp.CallToolRequest, in setUSBDeviceStateInput) (*mcp.CallToolResult, any, error) {
	if in.Device == "" {
		return nil, nil, fmt.Errorf("device is required")
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	if err := SetUSBDeviceState(ctx, client, in.Device, in.Enabled); err != nil {
		return nil, nil, fmt.Errorf("set USB device state: %w", err)
	}
	return text("%s %s.", in.Device, map[bool]string{true: "enabled", false: "disabled"}[in.Enabled]), nil, nil
}

func handleGetUSBEmulationState(ctx context.Context, _ *mcp.CallToolRequest, in kvmOnly) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	enabled, err := GetUSBEmulationState(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("get USB emulation state: %w", err)
	}
	return text("USB emulation enabled: %t", enabled), nil, nil
}

func handleSetUSBEmulationState(ctx context.Context, _ *mcp.CallToolRequest, in setUSBEmulationInput) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := makeClient(ctx, in.KVM)
	if err != nil {
		return nil, nil, err
	}

	if err := SetUSBEmulationState(ctx, client, in.Enabled); err != nil {
		return nil, nil, fmt.Errorf("set USB emulation state: %w", err)
	}
	return text("USB emulation %s.", map[bool]string{true: "enabled", false: "disabled"}[in.Enabled]), nil, nil
}
