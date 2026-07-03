package jetkvm

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// resultText extracts the text of the first TextContent block in a tool result.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected non-empty tool result content")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
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

		res, err := handleListVirtualMediaFiles(context.Background(), callToolRequest(nil))
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

		res, err := handleListVirtualMediaFiles(context.Background(), callToolRequest(nil))
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

		res, err := handleGetVirtualMediaState(context.Background(), callToolRequest(nil))
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

		res, err := handleGetVirtualMediaState(context.Background(), callToolRequest(nil))
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

		res, err := handleGetVirtualMediaState(context.Background(), callToolRequest(nil))
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

		res, err := handleCheckVirtualMediaURL(context.Background(), callToolRequest(map[string]interface{}{"url": "http://example.com/x.iso"}))
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

		res, err := handleCheckVirtualMediaURL(context.Background(), callToolRequest(map[string]interface{}{"url": "http://example.com/x.txt"}))
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

		res, err := handleMountVirtualMedia(context.Background(), callToolRequest(map[string]interface{}{
			"source": "storage", "filename": "ubuntu.iso", "mode": "cdrom",
		}))
		if err != nil {
			t.Fatalf("handleMountVirtualMedia: %v", err)
		}
		if text := resultText(t, res); !strings.Contains(text, "ubuntu.iso") || !strings.Contains(text, "storage") {
			t.Fatalf("unexpected text: %q", text)
		}
	})

	t.Run("from http with default mode", func(t *testing.T) {
		_, client := deviceRPCServer(t, func(method string) any { return nil })
		t.Setenv("JETKVM_HOST", client.baseURL)

		res, err := handleMountVirtualMedia(context.Background(), callToolRequest(map[string]interface{}{
			"source": "http", "url": "http://example.com/x.iso",
		}))
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

	res, err := handleUnmountVirtualMedia(context.Background(), callToolRequest(nil))
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

	res, err := handleDeleteVirtualMediaFile(context.Background(), callToolRequest(map[string]interface{}{"filename": "ubuntu.iso"}))
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

	res, err := handleGetUSBDevices(context.Background(), callToolRequest(nil))
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

	res, err := handleSetUSBDevices(context.Background(), callToolRequest(map[string]interface{}{
		"absolute_mouse": true,
		"relative_mouse": true,
		"keyboard":       true,
		"mass_storage":   true,
		"serial_console": false,
		"audio":          false,
	}))
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

	res, err := handleSetUSBDeviceState(context.Background(), callToolRequest(map[string]interface{}{
		"device": "massStorage", "enabled": false,
	}))
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

	res, err := handleGetUSBEmulationState(context.Background(), callToolRequest(nil))
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

	res, err := handleSetUSBEmulationState(context.Background(), callToolRequest(map[string]interface{}{"enabled": true}))
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

	if _, err := handleListVirtualMediaFiles(context.Background(), callToolRequest(nil)); err == nil {
		t.Fatal("expected error from handleListVirtualMediaFiles")
	}
	if _, err := handleGetVirtualMediaState(context.Background(), callToolRequest(nil)); err == nil {
		t.Fatal("expected error from handleGetVirtualMediaState")
	}
	if _, err := handleUnmountVirtualMedia(context.Background(), callToolRequest(nil)); err == nil {
		t.Fatal("expected error from handleUnmountVirtualMedia")
	}
	if _, err := handleDeleteVirtualMediaFile(context.Background(), callToolRequest(map[string]interface{}{"filename": "x"})); err == nil {
		t.Fatal("expected error from handleDeleteVirtualMediaFile")
	}
	if _, err := handleGetUSBDevices(context.Background(), callToolRequest(nil)); err == nil {
		t.Fatal("expected error from handleGetUSBDevices")
	}
	if _, err := handleGetUSBEmulationState(context.Background(), callToolRequest(nil)); err == nil {
		t.Fatal("expected error from handleGetUSBEmulationState")
	}
}
