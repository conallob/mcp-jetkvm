package jetkvm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
)

func TestDecodeRPCMessage(t *testing.T) {
	t.Run("valid response", func(t *testing.T) {
		resp, ok := decodeRPCMessage([]byte(`{"jsonrpc":"2.0","result":{"ok":true},"id":7}`))
		if !ok {
			t.Fatal("expected ok=true for a response with an id")
		}
		if resp.ID == nil || *resp.ID != 7 {
			t.Fatalf("unexpected id: %v", resp.ID)
		}
	})

	t.Run("response with error", func(t *testing.T) {
		resp, ok := decodeRPCMessage([]byte(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"not found"},"id":3}`))
		if !ok {
			t.Fatal("expected ok=true")
		}
		if resp.Error == nil || resp.Error.Code != -32601 {
			t.Fatalf("unexpected error: %+v", resp.Error)
		}
		if resp.Error.Error() == "" {
			t.Fatal("expected non-empty error string")
		}
	})

	t.Run("unsolicited event without id is ignored", func(t *testing.T) {
		_, ok := decodeRPCMessage([]byte(`{"jsonrpc":"2.0","method":"videoStateUpdate","params":{}}`))
		if ok {
			t.Fatal("expected ok=false for an event with no id")
		}
	})

	t.Run("malformed JSON is ignored", func(t *testing.T) {
		_, ok := decodeRPCMessage([]byte(`{not json`))
		if ok {
			t.Fatal("expected ok=false for malformed JSON")
		}
	})
}

// deviceRPCServer starts a test HTTP server that behaves like a JetKVM
// device for login and SDP exchange, and answers every offer with a real
// WebRTC peer connection that echoes back a canned JSON-RPC result for
// every request received on the "rpc" data channel.
func deviceRPCServer(t *testing.T, resultFor func(method string) any) (*httptest.Server, *Client) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webrtc/session", func(w http.ResponseWriter, r *http.Request) {
		var wire struct {
			SD string `json:"sd"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Errorf("decode offer envelope: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, err := base64.StdEncoding.DecodeString(wire.SD)
		if err != nil {
			t.Errorf("decode offer base64: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var offerEnv sdpEnvelope
		if err := json.Unmarshal(raw, &offerEnv); err != nil {
			t.Errorf("unmarshal offer: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		devicePC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Errorf("device NewPeerConnection: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		t.Cleanup(func() { devicePC.Close() })

		devicePC.OnDataChannel(func(dc *webrtc.DataChannel) {
			dc.OnMessage(func(msg webrtc.DataChannelMessage) {
				var req jsonrpcRequest
				if err := json.Unmarshal(msg.Data, &req); err != nil {
					return
				}
				result := resultFor(req.Method)
				resultRaw, _ := json.Marshal(result)
				resp := jsonrpcResponse{JSONRPC: "2.0", Result: resultRaw, ID: &req.ID}
				respRaw, _ := json.Marshal(resp)
				_ = dc.SendText(string(respRaw))
			})
		})

		if err := devicePC.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  offerEnv.SDP,
		}); err != nil {
			t.Errorf("device SetRemoteDescription: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		answer, err := devicePC.CreateAnswer(nil)
		if err != nil {
			t.Errorf("device CreateAnswer: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		gatherDone := webrtc.GatheringCompletePromise(devicePC)
		if err := devicePC.SetLocalDescription(answer); err != nil {
			t.Errorf("device SetLocalDescription: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		<-gatherDone

		answerRaw, _ := json.Marshal(sdpEnvelope{
			Type: "answer",
			SDP:  devicePC.LocalDescription().SDP,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"sd": base64.StdEncoding.EncodeToString(answerRaw),
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := NewClient(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return srv, client
}

func TestRPCCallEndToEnd(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any {
		switch method {
		case "getUsbEmulationState":
			return true
		default:
			return map[string]any{"echo": method}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var enabled bool
	if err := callRPC(ctx, client, "getUsbEmulationState", nil, &enabled); err != nil {
		t.Fatalf("callRPC: %v", err)
	}
	if !enabled {
		t.Fatal("expected enabled=true")
	}
}

func TestRPCCallRemoteError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webrtc/session", func(w http.ResponseWriter, r *http.Request) {
		var wire struct {
			SD string `json:"sd"`
		}
		json.NewDecoder(r.Body).Decode(&wire)
		raw, _ := base64.StdEncoding.DecodeString(wire.SD)
		var offerEnv sdpEnvelope
		json.Unmarshal(raw, &offerEnv)

		devicePC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatalf("device NewPeerConnection: %v", err)
		}
		t.Cleanup(func() { devicePC.Close() })

		devicePC.OnDataChannel(func(dc *webrtc.DataChannel) {
			dc.OnMessage(func(msg webrtc.DataChannelMessage) {
				var req jsonrpcRequest
				json.Unmarshal(msg.Data, &req)
				resp := jsonrpcResponse{
					JSONRPC: "2.0",
					Error:   &jsonrpcError{Code: -32602, Message: "invalid params"},
					ID:      &req.ID,
				}
				respRaw, _ := json.Marshal(resp)
				dc.SendText(string(respRaw))
			})
		})

		devicePC.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerEnv.SDP})
		answer, _ := devicePC.CreateAnswer(nil)
		gatherDone := webrtc.GatheringCompletePromise(devicePC)
		devicePC.SetLocalDescription(answer)
		<-gatherDone

		answerRaw, _ := json.Marshal(sdpEnvelope{Type: "answer", SDP: devicePC.LocalDescription().SDP})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"sd": base64.StdEncoding.EncodeToString(answerRaw)})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := NewClient(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = callRPC(ctx, client, "mountWithStorage", map[string]any{"filename": "x", "mode": "CDROM"}, nil)
	if err == nil {
		t.Fatal("expected error from remote RPC error response")
	}
}

func TestOpenRPCSessionNegotiationFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login-local", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webrtc/session", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := NewClient(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := openRPCSession(ctx, client); err == nil {
		t.Fatal("expected error when SDP exchange fails")
	}
}
