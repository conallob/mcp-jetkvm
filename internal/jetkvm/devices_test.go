package jetkvm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadDevices(t *testing.T) {
	t.Run("single device from JETKVM_HOST", func(t *testing.T) {
		t.Setenv("JETKVM_DEVICES", "")
		t.Setenv("JETKVM_HOST", "10.0.0.1")
		t.Setenv("JETKVM_PASSWORD", "pw")
		devices, err := loadDevices()
		if err != nil {
			t.Fatal(err)
		}
		if len(devices) != 1 || devices[0] != (Device{ID: defaultDeviceID, Host: "10.0.0.1", Password: "pw"}) {
			t.Fatalf("unexpected devices: %+v", devices)
		}
	})

	t.Run("neither configured", func(t *testing.T) {
		t.Setenv("JETKVM_DEVICES", "")
		t.Setenv("JETKVM_HOST", "")
		if _, err := loadDevices(); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("several devices, JETKVM_DEVICES wins over JETKVM_HOST", func(t *testing.T) {
		t.Setenv("JETKVM_HOST", "ignored")
		t.Setenv("JETKVM_DEVICES", "ctrl-1, ctrl-2")
		t.Setenv("JETKVM_CTRL_1_HOST", "a.example")
		t.Setenv("JETKVM_CTRL_1_PASSWORD", "pw1")
		t.Setenv("JETKVM_CTRL_2_HOST", "b.example")
		devices, err := loadDevices()
		if err != nil {
			t.Fatal(err)
		}
		want := []Device{{"ctrl-1", "a.example", "pw1"}, {"ctrl-2", "b.example", ""}}
		if len(devices) != 2 || devices[0] != want[0] || devices[1] != want[1] {
			t.Fatalf("got %+v, want %+v", devices, want)
		}
	})

	t.Run("listed device without a host", func(t *testing.T) {
		t.Setenv("JETKVM_DEVICES", "ctrl-1")
		t.Setenv("JETKVM_CTRL_1_HOST", "")
		if _, err := loadDevices(); err == nil || !strings.Contains(err.Error(), "JETKVM_CTRL_1_HOST") {
			t.Fatalf("expected an error naming JETKVM_CTRL_1_HOST, got %v", err)
		}
	})

	t.Run("IDs that map to the same variables", func(t *testing.T) {
		t.Setenv("JETKVM_DEVICES", "ctrl-1,ctrl_1")
		t.Setenv("JETKVM_CTRL_1_HOST", "a.example")
		if _, err := loadDevices(); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		t.Setenv("JETKVM_DEVICES", " , ")
		t.Setenv("JETKVM_HOST", "x")
		if _, err := loadDevices(); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestPickDevice(t *testing.T) {
	one := []Device{{ID: "only"}}
	two := []Device{{ID: "a"}, {ID: "b"}}

	if d, err := pickDevice(one, ""); err != nil || d.ID != "only" {
		t.Fatalf("single device, no id: %+v, %v", d, err)
	}
	if _, err := pickDevice(one, "other"); err == nil {
		t.Fatal("expected an error for an unknown id")
	}
	if d, err := pickDevice(two, "b"); err != nil || d.ID != "b" {
		t.Fatalf("pick b: %+v, %v", d, err)
	}
	if _, err := pickDevice(two, ""); err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Fatalf("expected an error listing the devices, got %v", err)
	}
}

// passwordServer accepts a login only with the given password and records it.
func passwordServer(t *testing.T, password string, logins *int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["password"] != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		*logins++
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMakeClientSelectsDevice(t *testing.T) {
	var loginsA, loginsB int
	a := passwordServer(t, "pw-a", &loginsA)
	b := passwordServer(t, "pw-b", &loginsB)
	t.Setenv("JETKVM_DEVICES", "a,b")
	t.Setenv("JETKVM_A_HOST", a.URL)
	t.Setenv("JETKVM_A_PASSWORD", "pw-a")
	t.Setenv("JETKVM_B_HOST", b.URL)
	t.Setenv("JETKVM_B_PASSWORD", "pw-b")

	if _, err := makeClient(context.Background(), "b"); err != nil {
		t.Fatalf("device b: %v", err)
	}
	if loginsA != 0 || loginsB != 1 {
		t.Fatalf("logins a=%d b=%d, want a=0 b=1", loginsA, loginsB)
	}
	if _, err := makeClient(context.Background(), ""); err == nil {
		t.Fatal("expected an error when no device is given and two are configured")
	}
}

func TestHandleListDevices(t *testing.T) {
	t.Setenv("JETKVM_DEVICES", "a,b")
	t.Setenv("JETKVM_A_HOST", "a.example")
	t.Setenv("JETKVM_A_PASSWORD", "secret-a")
	t.Setenv("JETKVM_B_HOST", "b.example")

	res, _, err := handleListDevices(context.Background(), nil, kvmOnly{})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "a: a.example") || !strings.Contains(text, "b: b.example") {
		t.Fatalf("unexpected text: %q", text)
	}
	if strings.Contains(text, "secret-a") {
		t.Fatal("list_devices must not reveal passwords")
	}
}
