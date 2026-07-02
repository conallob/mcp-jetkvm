package jetkvm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// loginOKServer returns a test server that accepts any login request.
func loginOKServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(mux)
}

func TestRequireFFmpeg(t *testing.T) {
	t.Run("found when in PATH", func(t *testing.T) {
		if requireFFmpeg() != nil {
			t.Skip("ffmpeg not available in this environment")
		}
		orig := os.Getenv("PATH")
		defer os.Setenv("PATH", orig)
		if err := requireFFmpeg(); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("error when not in PATH", func(t *testing.T) {
		orig := os.Getenv("PATH")
		defer os.Setenv("PATH", orig)
		os.Setenv("PATH", "")
		if err := requireFFmpeg(); err == nil {
			t.Fatal("expected error when PATH is empty, got nil")
		}
	})
}

func TestMakeClient(t *testing.T) {
	t.Run("missing JETKVM_HOST", func(t *testing.T) {
		os.Unsetenv("JETKVM_HOST")
		_, err := makeClient(context.Background())
		if err == nil {
			t.Fatal("expected error when JETKVM_HOST is unset")
		}
	})

	t.Run("connects with JETKVM_HOST set", func(t *testing.T) {
		srv := loginOKServer(t)
		defer srv.Close()
		os.Setenv("JETKVM_HOST", srv.URL)
		defer os.Unsetenv("JETKVM_HOST")

		c, err := makeClient(context.Background())
		if err != nil {
			t.Fatalf("makeClient: %v", err)
		}
		if c == nil {
			t.Fatal("expected non-nil client")
		}
	})
}

func callToolRequest(args map[string]interface{}) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

func TestHandleRecordVideoValidation(t *testing.T) {
	t.Run("missing output_path", func(t *testing.T) {
		if requireFFmpeg() != nil {
			t.Skip("ffmpeg not available")
		}
		req := callToolRequest(map[string]interface{}{})
		_, err := handleRecordVideo(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for missing output_path")
		}
	})

	t.Run("no ffmpeg", func(t *testing.T) {
		orig := os.Getenv("PATH")
		os.Setenv("PATH", "")
		defer os.Setenv("PATH", orig)

		req := callToolRequest(map[string]interface{}{"output_path": "/tmp/out.mp4"})
		_, err := handleRecordVideo(context.Background(), req)
		if err == nil {
			t.Fatal("expected error when ffmpeg is missing")
		}
	})
}

func TestHandleTakeScreenshotNoFFmpeg(t *testing.T) {
	orig := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", orig)

	req := callToolRequest(map[string]interface{}{})
	_, err := handleTakeScreenshot(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when ffmpeg is missing")
	}
}

func TestHandleRecordVideoDurationClamping(t *testing.T) {
	cases := []struct {
		name  string
		input interface{}
	}{
		{"below minimum", float64(0)},
		{"above maximum", float64(120)},
		{"default (no value)", nil},
		{"valid mid-range", float64(30)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if requireFFmpeg() != nil {
				t.Skip("ffmpeg not available")
			}
			os.Unsetenv("JETKVM_HOST")
			args := map[string]interface{}{"output_path": "/tmp/out.mp4"}
			if tc.input != nil {
				args["duration"] = tc.input
			}
			req := callToolRequest(args)
			_, err := handleRecordVideo(context.Background(), req)
			// We expect a "JETKVM_HOST" error — that means we passed
			// validation and clamping and reached makeClient.
			if err == nil {
				t.Fatal("expected error from makeClient, got nil")
			}
		})
	}
}

func TestHandleCheckVirtualMediaURLValidation(t *testing.T) {
	req := callToolRequest(map[string]interface{}{})
	_, err := handleCheckVirtualMediaURL(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestHandleMountVirtualMediaValidation(t *testing.T) {
	t.Run("invalid mode", func(t *testing.T) {
		req := callToolRequest(map[string]interface{}{"source": "storage", "filename": "x.iso", "mode": "bogus"})
		_, err := handleMountVirtualMedia(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for invalid mode")
		}
	})

	t.Run("invalid source", func(t *testing.T) {
		req := callToolRequest(map[string]interface{}{"source": "bogus"})
		_, err := handleMountVirtualMedia(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for invalid source")
		}
	})

	t.Run("storage source missing filename", func(t *testing.T) {
		req := callToolRequest(map[string]interface{}{"source": "storage"})
		_, err := handleMountVirtualMedia(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for missing filename")
		}
	})

	t.Run("http source missing url", func(t *testing.T) {
		req := callToolRequest(map[string]interface{}{"source": "http"})
		_, err := handleMountVirtualMedia(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for missing url")
		}
	})
}

func TestHandleDeleteVirtualMediaFileValidation(t *testing.T) {
	req := callToolRequest(map[string]interface{}{})
	_, err := handleDeleteVirtualMediaFile(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing filename")
	}
}

func TestHandleSetUSBDevicesValidation(t *testing.T) {
	req := callToolRequest(map[string]interface{}{
		"absolute_mouse": true,
		"relative_mouse": true,
		"keyboard":       true,
		"mass_storage":   true,
		// serial_console and audio omitted
	})
	_, err := handleSetUSBDevices(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing boolean fields")
	}
}

func TestHandleSetUSBDeviceStateValidation(t *testing.T) {
	t.Run("missing device", func(t *testing.T) {
		req := callToolRequest(map[string]interface{}{"enabled": true})
		_, err := handleSetUSBDeviceState(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for missing device")
		}
	})

	t.Run("missing enabled", func(t *testing.T) {
		req := callToolRequest(map[string]interface{}{"device": "massStorage"})
		_, err := handleSetUSBDeviceState(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for missing enabled")
		}
	})
}

func TestHandleSetUSBEmulationStateValidation(t *testing.T) {
	req := callToolRequest(map[string]interface{}{})
	_, err := handleSetUSBEmulationState(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing enabled")
	}
}

func TestNewPeerConnection(t *testing.T) {
	pc, err := newPeerConnection()
	if err != nil {
		t.Fatalf("newPeerConnection: %v", err)
	}
	if pc == nil {
		t.Fatal("expected non-nil PeerConnection")
	}
	pc.Close()
}
