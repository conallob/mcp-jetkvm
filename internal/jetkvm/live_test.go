package jetkvm

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
)

// TestLiveDevice exercises a real JetKVM end to end: login, signaling, the
// RPC data channel and an incoming video track. It is skipped unless a device
// is named, so CI never needs hardware:
//
//	JETKVM_LIVE_HOST=jetkvm-homelab-ctrl-2.local JETKVM_LIVE_PASSWORD=... \
//	  go test -run TestLiveDevice -v ./internal/jetkvm
//
// It only reads state and never sends input or mounts media.
func TestLiveDevice(t *testing.T) {
	host := os.Getenv("JETKVM_LIVE_HOST")
	if host == "" {
		t.Skip("JETKVM_LIVE_HOST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client, err := NewClient(ctx, host, os.Getenv("JETKVM_LIVE_PASSWORD"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	t.Run("RPC data channel", func(t *testing.T) {
		var raw json.RawMessage
		if err := callRPC(ctx, client, "getUsbEmulationState", nil, &raw); err != nil {
			t.Fatalf("callRPC: %v", err)
		}
		t.Logf("getUsbEmulationState = %s", raw)
	})

	t.Run("video track", func(t *testing.T) {
		pc, err := newPeerConnection()
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		got := make(chan struct{}, 1)
		pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			if track.Kind() == webrtc.RTPCodecTypeVideo {
				select {
				case got <- struct{}{}:
				default:
				}
			}
		})
		if err := negotiate(ctx, pc, client); err != nil {
			t.Fatalf("negotiate: %v", err)
		}
		select {
		case <-got:
		case <-time.After(20 * time.Second):
			t.Fatal("no video track arrived; signaling worked but media did not connect")
		}
	})
}
