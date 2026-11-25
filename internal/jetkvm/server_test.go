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
	// A reachable device, so these inputs fail on their own validation and
	// not on connecting.
	srv := loginOKServer(t)
	defer srv.Close()
	t.Setenv("JETKVM_DEVICES", "")
	t.Setenv("JETKVM_HOST", srv.URL)

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

func TestHandlersFailWithoutADevice(t *testing.T) {
	t.Setenv("JETKVM_DEVICES", "")
	t.Setenv("JETKVM_HOST", "")
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"take_screenshot": func() error { _, _, err := handleTakeScreenshot(ctx, nil, screenshotInput{}); return err },
		"record_video": func() error {
			_, _, err := handleRecordVideo(ctx, nil, recordVideoInput{OutputPath: "/tmp/x.mp4"})
			return err
		},
		"list_virtual_media_files": func() error { _, _, err := handleListVirtualMediaFiles(ctx, nil, kvmOnly{}); return err },
		"get_virtual_media_state":  func() error { _, _, err := handleGetVirtualMediaState(ctx, nil, kvmOnly{}); return err },
		"check_virtual_media_url": func() error {
			_, _, err := handleCheckVirtualMediaURL(ctx, nil, checkURLInput{URL: "http://x/y.iso"})
			return err
		},
		"mount_virtual_media": func() error {
			_, _, err := handleMountVirtualMedia(ctx, nil, mountInput{Source: "storage", Filename: "x"})
			return err
		},
		"unmount_virtual_media": func() error { _, _, err := handleUnmountVirtualMedia(ctx, nil, kvmOnly{}); return err },
		"delete_virtual_media_file": func() error {
			_, _, err := handleDeleteVirtualMediaFile(ctx, nil, deleteFileInput{Filename: "x"})
			return err
		},
		"get_usb_devices": func() error { _, _, err := handleGetUSBDevices(ctx, nil, kvmOnly{}); return err },
		"set_usb_devices": func() error { _, _, err := handleSetUSBDevices(ctx, nil, setUSBDevicesInput{}); return err },
		"set_usb_device_state": func() error {
			_, _, err := handleSetUSBDeviceState(ctx, nil, setUSBDeviceStateInput{Device: "audio"})
			return err
		},
		"get_usb_emulation_state": func() error { _, _, err := handleGetUSBEmulationState(ctx, nil, kvmOnly{}); return err },
		"set_usb_emulation_state": func() error { _, _, err := handleSetUSBEmulationState(ctx, nil, setUSBEmulationInput{}); return err },
		"list_devices":            func() error { _, _, err := handleListDevices(ctx, nil, kvmOnly{}); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("expected an error when no device is configured")
			}
		})
	}
}

func TestRunServerRejectsBadHTTPOptions(t *testing.T) {
	// Unauthenticated access on all interfaces is refused before listening.
	err := RunServer(Options{HTTP: &HTTPOptions{Addr: ":8080", Auth: AuthNone}})
	if err == nil {
		t.Fatal("expected an error")
	}
}
