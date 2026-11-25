package jetkvm

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// resultText extracts the text of the first TextContent block in a tool result.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected non-empty tool result content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func TestHandleListVirtualMediaFilesSuccess(t *testing.T) {
	t.Run("with files", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any {
			switch method {
			case "listStorageFiles":
				return StorageFiles{Files: []StorageFile{{Filename: "ubuntu.iso", Size: 123, CreatedAt: "2026-01-01T00:00:00Z"}}}
			case "getStorageSpace":
				return StorageSpace{BytesUsed: 10, BytesFree: 90}
			default:
				return nil
			}
		})
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleListVirtualMediaFiles(context.Background(), nil, kvmOnly{})
		if err != nil {
			t.Fatalf("handleListVirtualMediaFiles: %v", err)
		}
		text := resultText(t, res)
		if !strings.Contains(text, "ubuntu.iso") || !strings.Contains(text, "10 bytes used") {
			t.Fatalf("unexpected text: %q", text)
		}
	})

	t.Run("no files", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any {
			switch method {
			case "listStorageFiles":
				return StorageFiles{}
			case "getStorageSpace":
				return StorageSpace{BytesUsed: 0, BytesFree: 100}
			default:
				return nil
			}
		})
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleListVirtualMediaFiles(context.Background(), nil, kvmOnly{})
		if err != nil {
			t.Fatalf("handleListVirtualMediaFiles: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "No image files") {
			t.Fatalf("unexpected text: %q", text)
		}
	})
}

func TestHandleGetVirtualMediaStateSuccess(t *testing.T) {
	t.Run("mounted", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any {
			return VirtualMediaState{Source: SourceStorage, Mode: ModeCDROM, Filename: "ubuntu.iso", Size: 42}
		})
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleGetVirtualMediaState(context.Background(), nil, kvmOnly{})
		if err != nil {
			t.Fatalf("handleGetVirtualMediaState: %v", err)
		}
		text := resultText(t, res)
		if !strings.Contains(text, "ubuntu.iso") || !strings.Contains(text, "CDROM") {
			t.Fatalf("unexpected text: %q", text)
		}
	})

	t.Run("not mounted", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any { return VirtualMediaState{} })
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleGetVirtualMediaState(context.Background(), nil, kvmOnly{})
		if err != nil {
			t.Fatalf("handleGetVirtualMediaState: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "No virtual media") {
			t.Fatalf("unexpected text: %q", text)
		}
	})

	t.Run("mounted via HTTP shows URL", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any {
			return VirtualMediaState{Source: SourceHTTP, Mode: ModeDisk, URL: "http://example.com/x.iso", Size: 7}
		})
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleGetVirtualMediaState(context.Background(), nil, kvmOnly{})
		if err != nil {
			t.Fatalf("handleGetVirtualMediaState: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "http://example.com/x.iso") {
			t.Fatalf("unexpected text: %q", text)
		}
	})
}

func TestHandleCheckVirtualMediaURLSuccess(t *testing.T) {
	t.Run("usable", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any {
			return VirtualMediaURLInfo{Usable: true, Size: 555}
		})
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleCheckVirtualMediaURL(context.Background(), nil, checkURLInput{URL: "http://example.com/x.iso"})
		if err != nil {
			t.Fatalf("handleCheckVirtualMediaURL: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "mountable directly") {
			t.Fatalf("unexpected text: %q", text)
		}
	})

	t.Run("not usable", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any {
			return VirtualMediaURLInfo{Usable: false, Reason: "not a supported format"}
		})
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleCheckVirtualMediaURL(context.Background(), nil, checkURLInput{URL: "http://example.com/x.txt"})
		if err != nil {
			t.Fatalf("handleCheckVirtualMediaURL: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "not a supported format") {
			t.Fatalf("unexpected text: %q", text)
		}
	})
}

func TestHandleMountVirtualMediaSuccess(t *testing.T) {
	t.Run("from storage", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any { return nil })
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleMountVirtualMedia(context.Background(), nil, mountInput{
			Source: "storage", Filename: "ubuntu.iso", Mode: "cdrom",
		})
		if err != nil {
			t.Fatalf("handleMountVirtualMedia: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "ubuntu.iso") || !strings.Contains(text, "storage") {
			t.Fatalf("unexpected text: %q", text)
		}
	})

	t.Run("as a disk", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any { return nil })
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleMountVirtualMedia(context.Background(), nil, mountInput{
			Source: "storage", Filename: "disk.img", Mode: "disk",
		})
		if err != nil {
			t.Fatalf("handleMountVirtualMedia: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "as disk") {
			t.Fatalf("unexpected text: %q", text)
		}
	})

	t.Run("from http with default mode", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any { return nil })
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, _, err := handleMountVirtualMedia(context.Background(), nil, mountInput{
			Source: "http", URL: "http://example.com/x.iso",
		})
		if err != nil {
			t.Fatalf("handleMountVirtualMedia: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "http://example.com/x.iso") || !strings.Contains(text, "HTTP") {
			t.Fatalf("unexpected text: %q", text)
		}
	})
}

func TestHandleUnmountVirtualMediaSuccess(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any { return nil })
	t.Setenv("JETKVM_HOST", client.baseURL)

	res, _, err := handleUnmountVirtualMedia(context.Background(), nil, kvmOnly{})
	if err != nil {
		t.Fatalf("handleUnmountVirtualMedia: %v", err)
	}
	if text := resultText(t, res); !strings.Contains(text, "unmounted") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestHandleDeleteVirtualMediaFileSuccess(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any { return nil })
	t.Setenv("JETKVM_HOST", client.baseURL)

	res, _, err := handleDeleteVirtualMediaFile(context.Background(), nil, deleteFileInput{Filename: "ubuntu.iso"})
	if err != nil {
		t.Fatalf("handleDeleteVirtualMediaFile: %v", err)
	}
	if text := resultText(t, res); !strings.Contains(text, "Deleted ubuntu.iso") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestHandleGetUSBDevicesSuccess(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any {
		return USBDevices{AbsoluteMouse: true, Keyboard: true, MassStorage: true}
	})
	t.Setenv("JETKVM_HOST", client.baseURL)

	res, _, err := handleGetUSBDevices(context.Background(), nil, kvmOnly{})
	if err != nil {
		t.Fatalf("handleGetUSBDevices: %v", err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "absolute_mouse=true") || !strings.Contains(text, "audio=false") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestHandleSetUSBDevicesSuccess(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any { return nil })
	t.Setenv("JETKVM_HOST", client.baseURL)

	res, _, err := handleSetUSBDevices(context.Background(), nil, setUSBDevicesInput{
		AbsoluteMouse: true,
		RelativeMouse: true,
		Keyboard:      true,
		MassStorage:   true,
	})
	if err != nil {
		t.Fatalf("handleSetUSBDevices: %v", err)
	}
	if text := resultText(t, res); !strings.Contains(text, "updated") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestHandleSetUSBDeviceStateSuccess(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any { return nil })
	t.Setenv("JETKVM_HOST", client.baseURL)

	res, _, err := handleSetUSBDeviceState(context.Background(), nil, setUSBDeviceStateInput{Device: "massStorage"})
	if err != nil {
		t.Fatalf("handleSetUSBDeviceState: %v", err)
	}
	if text := resultText(t, res); !strings.Contains(text, "massStorage disabled") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestHandleGetUSBEmulationStateSuccess(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any { return true })
	t.Setenv("JETKVM_HOST", client.baseURL)

	res, _, err := handleGetUSBEmulationState(context.Background(), nil, kvmOnly{})
	if err != nil {
		t.Fatalf("handleGetUSBEmulationState: %v", err)
	}
	if text := resultText(t, res); !strings.Contains(text, "true") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestHandleSetUSBEmulationStateSuccess(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any { return nil })
	t.Setenv("JETKVM_HOST", client.baseURL)

	res, _, err := handleSetUSBEmulationState(context.Background(), nil, setUSBEmulationInput{Enabled: true})
	if err != nil {
		t.Fatalf("handleSetUSBEmulationState: %v", err)
	}
	if text := resultText(t, res); !strings.Contains(text, "enabled") {
		t.Fatalf("unexpected text: %q", text)
	}
}

// RPC-layer error propagation through the handlers.
func TestHandleVirtualMediaAndUSBRPCErrorPropagation(t *testing.T) {
	_, client := errorRPCServer(t)
	t.Setenv("JETKVM_HOST", client.baseURL)

	if _, _, err := handleListVirtualMediaFiles(context.Background(), nil, kvmOnly{}); err == nil {
		t.Fatal("expected error from handleListVirtualMediaFiles")
	}
	if _, _, err := handleGetVirtualMediaState(context.Background(), nil, kvmOnly{}); err == nil {
		t.Fatal("expected error from handleGetVirtualMediaState")
	}
	if _, _, err := handleUnmountVirtualMedia(context.Background(), nil, kvmOnly{}); err == nil {
		t.Fatal("expected error from handleUnmountVirtualMedia")
	}
	if _, _, err := handleDeleteVirtualMediaFile(context.Background(), nil, deleteFileInput{Filename: "x"}); err == nil {
		t.Fatal("expected error from handleDeleteVirtualMediaFile")
	}
	if _, _, err := handleGetUSBDevices(context.Background(), nil, kvmOnly{}); err == nil {
		t.Fatal("expected error from handleGetUSBDevices")
	}
	if _, _, err := handleGetUSBEmulationState(context.Background(), nil, kvmOnly{}); err == nil {
		t.Fatal("expected error from handleGetUSBEmulationState")
	}
	if _, _, err := handleCheckVirtualMediaURL(context.Background(), nil, checkURLInput{URL: "http://example.com/x.iso"}); err == nil {
		t.Fatal("expected error from handleCheckVirtualMediaURL")
	}
	if _, _, err := handleMountVirtualMedia(context.Background(), nil, mountInput{Source: "storage", Filename: "x.iso"}); err == nil {
		t.Fatal("expected error from handleMountVirtualMedia (storage)")
	}
	if _, _, err := handleMountVirtualMedia(context.Background(), nil, mountInput{Source: "http", URL: "http://example.com/x.iso", Mode: "disk"}); err == nil {
		t.Fatal("expected error from handleMountVirtualMedia (http)")
	}
	if _, _, err := handleSetUSBDevices(context.Background(), nil, setUSBDevicesInput{}); err == nil {
		t.Fatal("expected error from handleSetUSBDevices")
	}
	if _, _, err := handleSetUSBDeviceState(context.Background(), nil, setUSBDeviceStateInput{Device: "audio"}); err == nil {
		t.Fatal("expected error from handleSetUSBDeviceState")
	}
	if _, _, err := handleSetUSBEmulationState(context.Background(), nil, setUSBEmulationInput{}); err == nil {
		t.Fatal("expected error from handleSetUSBEmulationState")
	}
}
