package jetkvm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
		_, err := makeClient(context.Background(), "")
		if err == nil {
			t.Fatal("expected error when JETKVM_HOST is unset")
		}
	})

	t.Run("connects with JETKVM_HOST set", func(t *testing.T) {
		srv := loginOKServer(t)
		defer srv.Close()
		os.Setenv("JETKVM_HOST", srv.URL)
		defer os.Unsetenv("JETKVM_HOST")

		c, err := makeClient(context.Background(), "")
		if err != nil {
			t.Fatalf("makeClient: %v", err)
		}
		if c == nil {
			t.Fatal("expected non-nil client")
		}
	})
}

func TestHandleRecordVideoValidation(t *testing.T) {
	t.Run("missing output_path", func(t *testing.T) {
		if requireFFmpeg() != nil {
			t.Skip("ffmpeg not available")
		}
		if _, _, err := handleRecordVideo(context.Background(), nil, recordVideoInput{}); err == nil {
			t.Fatal("expected error for missing output_path")
		}
	})

	t.Run("no ffmpeg", func(t *testing.T) {
		t.Setenv("PATH", "")
		if _, _, err := handleRecordVideo(context.Background(), nil, recordVideoInput{OutputPath: "/tmp/out.mp4"}); err == nil {
			t.Fatal("expected error when ffmpeg is missing")
		}
	})
}

func TestHandleTakeScreenshotNoFFmpeg(t *testing.T) {
	t.Setenv("PATH", "")
	if _, _, err := handleTakeScreenshot(context.Background(), nil, screenshotInput{}); err == nil {
		t.Fatal("expected error when ffmpeg is missing")
	}
}

func TestHandleRecordVideoDurationClamping(t *testing.T) {
	for name, duration := range map[string]float64{
		"below minimum":      0,
		"above maximum":      120,
		"default (no value)": 0,
		"valid mid-range":    30,
	} {
		t.Run(name, func(t *testing.T) {
			if requireFFmpeg() != nil {
				t.Skip("ffmpeg not available")
			}
			t.Setenv("JETKVM_DEVICES", "")
			t.Setenv("JETKVM_HOST", "")
			// No device is configured, so reaching makeClient fails: that
			// means validation and clamping were passed.
			_, _, err := handleRecordVideo(context.Background(), nil, recordVideoInput{OutputPath: "/tmp/out.mp4", Duration: duration})
			if err == nil {
				t.Fatal("expected error from makeClient, got nil")
			}
		})
	}
}

func TestHandleCheckVirtualMediaURLValidation(t *testing.T) {
	if _, _, err := handleCheckVirtualMediaURL(context.Background(), nil, checkURLInput{}); err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestHandleMountVirtualMediaValidation(t *testing.T) {
	for name, in := range map[string]mountInput{
		"invalid mode":                {Source: "storage", Filename: "x.iso", Mode: "bogus"},
		"invalid source":              {Source: "bogus"},
		"storage source missing file": {Source: "storage"},
		"http source missing url":     {Source: "http"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := handleMountVirtualMedia(context.Background(), nil, in); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestHandleDeleteVirtualMediaFileValidation(t *testing.T) {
	if _, _, err := handleDeleteVirtualMediaFile(context.Background(), nil, deleteFileInput{}); err == nil {
		t.Fatal("expected error for missing filename")
	}
}

func TestHandleSetUSBDeviceStateValidation(t *testing.T) {
	if _, _, err := handleSetUSBDeviceState(context.Background(), nil, setUSBDeviceStateInput{Enabled: true}); err == nil {
		t.Fatal("expected error for missing device")
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
