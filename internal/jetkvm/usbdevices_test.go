package jetkvm

import (
	"context"
	"testing"
	"time"
)

func TestUSBDevicesRPCCalls(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any {
		switch method {
		case "getUsbDevices":
			return USBDevices{AbsoluteMouse: true, Keyboard: true, MassStorage: true}
		case "getUsbEmulationState":
			return true
		default:
			return nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	devices, err := GetUSBDevices(ctx, client)
	if err != nil {
		t.Fatalf("GetUSBDevices: %v", err)
	}
	if !devices.AbsoluteMouse || !devices.Keyboard || !devices.MassStorage || devices.Audio {
		t.Fatalf("unexpected devices: %+v", devices)
	}

	enabled, err := GetUSBEmulationState(ctx, client)
	if err != nil {
		t.Fatalf("GetUSBEmulationState: %v", err)
	}
	if !enabled {
		t.Fatal("expected emulation state to be true")
	}
}

func TestUSBDevicesMutationRPCCalls(t *testing.T) {
	calledMethods := map[string]bool{}
	_, client := deviceRPCServer(t, func(method string) any {
		calledMethods[method] = true
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := SetUSBDevices(ctx, client, USBDevices{Keyboard: true, MassStorage: true}); err != nil {
		t.Fatalf("SetUSBDevices: %v", err)
	}
	if err := SetUSBDeviceState(ctx, client, "massStorage", false); err != nil {
		t.Fatalf("SetUSBDeviceState: %v", err)
	}
	if err := SetUSBEmulationState(ctx, client, false); err != nil {
		t.Fatalf("SetUSBEmulationState: %v", err)
	}

	for _, method := range []string{"setUsbDevices", "setUsbDeviceState", "setUsbEmulationState"} {
		if !calledMethods[method] {
			t.Errorf("expected %s to be called", method)
		}
	}
}
