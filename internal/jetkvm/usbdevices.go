package jetkvm

import "context"

// USBDevices reports which USB gadget device classes the JetKVM presents to
// the connected host. MassStorage is what makes mounted virtual media appear
// as a bootable USB drive.
type USBDevices struct {
	AbsoluteMouse bool `json:"absolute_mouse"`
	RelativeMouse bool `json:"relative_mouse"`
	Keyboard      bool `json:"keyboard"`
	MassStorage   bool `json:"mass_storage"`
	SerialConsole bool `json:"serial_console"`
	Audio         bool `json:"audio"`
}

// usbDeviceNames enumerates the valid `device` values for SetUSBDeviceState.
var usbDeviceNames = []string{
	"absoluteMouse", "relativeMouse", "keyboard", "massStorage", "serialConsole", "audio",
}

// GetUSBDevices returns which USB gadget device classes are currently enabled.
func GetUSBDevices(ctx context.Context, client *Client) (*USBDevices, error) {
	var devices USBDevices
	if err := callRPC(ctx, client, "getUsbDevices", nil, &devices); err != nil {
		return nil, err
	}
	return &devices, nil
}

// SetUSBDevices replaces the full set of enabled USB gadget device classes.
func SetUSBDevices(ctx context.Context, client *Client, devices USBDevices) error {
	return callRPC(ctx, client, "setUsbDevices", map[string]any{
		"absolute_mouse": devices.AbsoluteMouse,
		"relative_mouse": devices.RelativeMouse,
		"keyboard":       devices.Keyboard,
		"mass_storage":   devices.MassStorage,
		"serial_console": devices.SerialConsole,
		"audio":          devices.Audio,
	}, nil)
}

// SetUSBDeviceState enables or disables a single USB gadget device class
// (one of usbDeviceNames) without affecting the others.
func SetUSBDeviceState(ctx context.Context, client *Client, device string, enabled bool) error {
	return callRPC(ctx, client, "setUsbDeviceState", map[string]any{
		"device":  device,
		"enabled": enabled,
	}, nil)
}

// GetUSBEmulationState reports whether the USB Device Controller is
// currently bound (i.e. the JetKVM is presenting any USB device to the host at all).
func GetUSBEmulationState(ctx context.Context, client *Client) (bool, error) {
	var enabled bool
	if err := callRPC(ctx, client, "getUsbEmulationState", nil, &enabled); err != nil {
		return false, err
	}
	return enabled, nil
}

// SetUSBEmulationState binds or unbinds the USB Device Controller, turning
// USB emulation to the host on or off entirely.
func SetUSBEmulationState(ctx context.Context, client *Client, enabled bool) error {
	return callRPC(ctx, client, "setUsbEmulationState", map[string]any{"enabled": enabled}, nil)
}
