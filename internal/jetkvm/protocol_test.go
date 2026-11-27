package jetkvm

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect returns a client session talking to a fresh server over an
// in-memory transport, so tests exercise the real tool registration.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := newMCPServer("test").Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// The server reports the version it was built with, not a constant, and says
// "dev" when it isn't told one (a plain `go build`).
func TestServerVersion(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"given", "1.2.3", "1.2.3"},
		{"default", "", "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			clientTransport, serverTransport := mcp.NewInMemoryTransports()
			serverSession, err := newMCPServer(tc.in).Connect(ctx, serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = serverSession.Close() }()
			session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			if got := session.InitializeResult().ServerInfo.Version; got != tc.want {
				t.Fatalf("ServerInfo.Version = %q, want %q", got, tc.want)
			}
		})
	}
}

type toolSchema struct {
	Properties map[string]struct {
		Enum []string `json:"enum"`
	} `json:"properties"`
	Required []string `json:"required"`
}

func schemas(t *testing.T) map[string]toolSchema {
	t.Helper()
	listed, err := connect(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]toolSchema{}
	for _, tool := range listed.Tools {
		raw, _ := json.Marshal(tool.InputSchema)
		var schema toolSchema
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		out[tool.Name] = schema
	}
	return out
}

func TestToolSchemas(t *testing.T) {
	tools := schemas(t)
	if len(tools) != 14 {
		t.Fatalf("got %d tools, want 14", len(tools))
	}

	for name, schema := range tools {
		if _, ok := schema.Properties["kvm"]; !ok {
			t.Errorf("%s: no kvm argument", name)
		}
		for _, required := range schema.Required {
			if required == "kvm" {
				t.Errorf("%s: kvm must be optional", name)
			}
		}
	}

	enums := map[string]map[string][]string{
		"mount_virtual_media":  {"source": {"storage", "http"}, "mode": {"cdrom", "disk"}},
		"set_usb_device_state": {"device": usbDeviceNames},
	}
	for tool, properties := range enums {
		for property, want := range properties {
			got := tools[tool].Properties[property].Enum
			if len(got) != len(want) {
				t.Errorf("%s.%s enum = %v, want %v", tool, property, got, want)
			}
		}
	}

	wantRequired := map[string][]string{
		"record_video":            {"output_path"},
		"check_virtual_media_url": {"url"},
		"mount_virtual_media":     {"source"},
		"set_usb_device_state":    {"device", "enabled"},
		"set_usb_emulation_state": {"enabled"},
		"set_usb_devices":         {"absolute_mouse", "relative_mouse", "keyboard", "mass_storage", "serial_console", "audio"},
	}
	for tool, want := range wantRequired {
		got := map[string]bool{}
		for _, name := range tools[tool].Required {
			got[name] = true
		}
		for _, name := range want {
			if !got[name] {
				t.Errorf("%s: %s should be required, required = %v", tool, name, tools[tool].Required)
			}
		}
	}
}

func TestCallToolOverProtocol(t *testing.T) {
	t.Setenv("JETKVM_DEVICES", "a,b")
	t.Setenv("JETKVM_A_HOST", "a.example")
	t.Setenv("JETKVM_B_HOST", "b.example")
	session := connect(t)
	ctx := context.Background()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_devices", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_devices: %v, %+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != "a: a.example\nb: b.example\n" {
		t.Fatalf("unexpected text: %q", text)
	}

	t.Run("several devices and no kvm is an error result", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_usb_emulation_state", Arguments: map[string]any{}})
		if err != nil || !res.IsError {
			t.Fatalf("want an error result, got %v, %+v", err, res)
		}
	})

	t.Run("a missing required argument is rejected before the handler", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "set_usb_devices",
			Arguments: map[string]any{"kvm": "a", "keyboard": true},
		})
		if err == nil && !res.IsError {
			t.Fatal("expected the call to be rejected")
		}
	})
}
