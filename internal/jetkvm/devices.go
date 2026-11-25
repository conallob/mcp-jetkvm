package jetkvm

import (
	"fmt"
	"os"
	"strings"
)

// defaultDeviceID names the device configured through JETKVM_HOST.
const defaultDeviceID = "default"

// Device is one configured JetKVM.
type Device struct {
	ID       string
	Host     string
	Password string
}

// loadDevices reads the device list from the environment.
//
// By default one server talks to one JetKVM, configured with JETKVM_HOST and
// JETKVM_PASSWORD. Setting JETKVM_DEVICES to a comma-separated list of IDs
// switches to several devices, each configured with JETKVM_<ID>_HOST and
// JETKVM_<ID>_PASSWORD, where <ID> is the ID upper-cased with every character
// other than A-Z and 0-9 replaced by "_". JETKVM_DEVICES takes precedence over
// JETKVM_HOST.
func loadDevices() ([]Device, error) {
	list := os.Getenv("JETKVM_DEVICES")
	if strings.TrimSpace(list) == "" {
		host := os.Getenv("JETKVM_HOST")
		if host == "" {
			return nil, fmt.Errorf("JETKVM_HOST environment variable is required (or set JETKVM_DEVICES for several devices)")
		}
		return []Device{{ID: defaultDeviceID, Host: host, Password: os.Getenv("JETKVM_PASSWORD")}}, nil
	}

	var devices []Device
	seen := map[string]string{}
	for _, id := range strings.Split(list, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		prefix := "JETKVM_" + envName(id) + "_"
		if other, dup := seen[prefix]; dup {
			return nil, fmt.Errorf("JETKVM_DEVICES: %q and %q map to the same variables (%sHOST)", other, id, prefix)
		}
		seen[prefix] = id
		host := os.Getenv(prefix + "HOST")
		if host == "" {
			return nil, fmt.Errorf("device %q: %sHOST environment variable is required", id, prefix)
		}
		devices = append(devices, Device{ID: id, Host: host, Password: os.Getenv(prefix + "PASSWORD")})
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("JETKVM_DEVICES is set but lists no devices")
	}
	return devices, nil
}

// envName turns a device ID into the form used in environment variable names.
func envName(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, id)
}

// pickDevice returns the device a tool call is addressed to. id may be empty
// only when exactly one device is configured.
func pickDevice(devices []Device, id string) (Device, error) {
	if id == "" {
		if len(devices) == 1 {
			return devices[0], nil
		}
		return Device{}, fmt.Errorf("kvm is required: %d JetKVMs are configured (%s)", len(devices), deviceIDs(devices))
	}
	for _, d := range devices {
		if d.ID == id {
			return d, nil
		}
	}
	return Device{}, fmt.Errorf("unknown device %q: configured devices are %s", id, deviceIDs(devices))
}

func deviceIDs(devices []Device) string {
	ids := make([]string, len(devices))
	for i, d := range devices {
		ids[i] = d.ID
	}
	return strings.Join(ids, ", ")
}
