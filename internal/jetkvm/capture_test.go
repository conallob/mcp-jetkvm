package jetkvm

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

func TestParseNALUnits(t *testing.T) {
	startCode := []byte{0, 0, 0, 1}

	t.Run("empty input", func(t *testing.T) {
		if got := parseNALUnits(nil); len(got) != 0 {
			t.Fatalf("expected empty, got %d units", len(got))
		}
	})

	t.Run("single unit", func(t *testing.T) {
		data := append(startCode, 0x67, 0xAA, 0xBB)
		units := parseNALUnits(data)
		if len(units) != 1 {
			t.Fatalf("expected 1 unit, got %d", len(units))
		}
		if got := units[0]; string(got) != string(data) {
			t.Fatalf("unit mismatch: %v vs %v", got, data)
		}
	})

	t.Run("two units", func(t *testing.T) {
		unit1 := append(startCode, 0x67, 0x01)
		unit2 := append(startCode, 0x68, 0x02)
		data := append(unit1, unit2...)
		units := parseNALUnits(data)
		if len(units) != 2 {
			t.Fatalf("expected 2 units, got %d", len(units))
		}
	})

	t.Run("three units", func(t *testing.T) {
		build := func(b byte) []byte { return append(append([]byte(nil), startCode...), b) }
		data := append(build(0x67), append(build(0x68), build(0x65)...)...)
		units := parseNALUnits(data)
		if len(units) != 3 {
			t.Fatalf("expected 3 units, got %d", len(units))
		}
	})
}

func TestNALUnitType(t *testing.T) {
	tests := []struct {
		name     string
		unit     []byte
		expected byte
	}{
		{"too short", []byte{0, 0, 0, 1}, 0},
		{"SPS (7)", []byte{0, 0, 0, 1, 0x67}, nalSPS},
		{"PPS (8)", []byte{0, 0, 0, 1, 0x68}, nalPPS},
		{"IDR (5)", []byte{0, 0, 0, 1, 0x65}, nalIDR},
		{"masks top bits", []byte{0, 0, 0, 1, 0xFF}, 0x1F},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nalUnitType(tc.unit); got != tc.expected {
				t.Fatalf("nalUnitType(%v) = %d, want %d", tc.unit, got, tc.expected)
			}
		})
	}
}

// TestCaptureKeyframeDirect exercises the real WebRTC/RTP assembly logic in
// captureKeyframe using two in-process peer connections on loopback, without
// needing ffmpeg: it writes synthetic single-NALU H.264 RTP packets (SPS,
// PPS, IDR) from a "device" track and verifies captureKeyframe reassembles
// them into an Annex-B keyframe.
func TestCaptureKeyframeDirect(t *testing.T) {
	devicePC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("device NewPeerConnection: %v", err)
	}
	defer devicePC.Close()

	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "pion")
	if err != nil {
		t.Fatalf("NewTrackLocalStaticRTP: %v", err)
	}

	sender, err := devicePC.AddTrack(track)
	if err != nil {
		t.Fatalf("AddTrack: %v", err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := sender.Read(buf); rtcpErr != nil {
				return
			}
		}
	}()

	clientPC, err := newPeerConnection()
	if err != nil {
		t.Fatalf("newPeerConnection: %v", err)
	}
	defer clientPC.Close()

	trackCh := make(chan *webrtc.TrackRemote, 1)
	clientPC.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		select {
		case trackCh <- tr:
		default:
		}
	})

	if _, err := clientPC.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("AddTransceiverFromKind: %v", err)
	}

	offer, err := clientPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	clientGatherDone := webrtc.GatheringCompletePromise(clientPC)
	if err := clientPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("client SetLocalDescription: %v", err)
	}
	<-clientGatherDone

	if err := devicePC.SetRemoteDescription(*clientPC.LocalDescription()); err != nil {
		t.Fatalf("device SetRemoteDescription: %v", err)
	}
	answer, err := devicePC.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("device CreateAnswer: %v", err)
	}
	deviceGatherDone := webrtc.GatheringCompletePromise(devicePC)
	if err := devicePC.SetLocalDescription(answer); err != nil {
		t.Fatalf("device SetLocalDescription: %v", err)
	}
	<-deviceGatherDone

	if err := clientPC.SetRemoteDescription(*devicePC.LocalDescription()); err != nil {
		t.Fatalf("client SetRemoteDescription: %v", err)
	}

	// Replay the SPS/PPS/IDR triplet on a timer until the reader has what it
	// needs; captureKeyframe re-syncs on any full triplet, so early packets
	// lost during ICE/DTLS setup don't matter.
	stopWrite := make(chan struct{})
	defer close(stopWrite)
	go func() {
		var seq uint16
		writePkt := func(payload []byte, marker bool) {
			pkt := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					Marker:         marker,
					PayloadType:    96,
					SequenceNumber: seq,
					Timestamp:      uint32(seq) * 3000,
					SSRC:           1,
				},
				Payload: payload,
			}
			seq++
			_ = track.WriteRTP(pkt)
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWrite:
				return
			case <-ticker.C:
				writePkt([]byte{0x67, 0xAA, 0xBB}, false) // SPS
				writePkt([]byte{0x68, 0xCC}, false)       // PPS
				writePkt([]byte{0x65, 0xDD, 0xEE}, true)  // IDR
			}
		}
	}()

	var remoteTrack *webrtc.TrackRemote
	select {
	case remoteTrack = <-trackCh:
	case <-time.After(10 * time.Second):
		t.Fatal("no remote track received")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	frame, err := captureKeyframe(ctx, remoteTrack)
	if err != nil {
		t.Fatalf("captureKeyframe: %v", err)
	}

	units := parseNALUnits(frame)
	if len(units) != 3 {
		t.Fatalf("expected 3 NAL units, got %d: %x", len(units), frame)
	}
	if nalUnitType(units[0]) != nalSPS {
		t.Errorf("unit 0: expected SPS, got %d", nalUnitType(units[0]))
	}
	if nalUnitType(units[1]) != nalPPS {
		t.Errorf("unit 1: expected PPS, got %d", nalUnitType(units[1]))
	}
	if nalUnitType(units[2]) != nalIDR {
		t.Errorf("unit 2: expected IDR, got %d", nalUnitType(units[2]))
	}
}

// answeringVideoServer is a fake device that answers every offer with a real
// WebRTC peer connection that never sends any media. Used to exercise
// negotiate()'s success path and the ctx-timeout branches of
// TakeScreenshot/RecordVideo without needing ffmpeg.
func answeringVideoServer(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	return newFakeDevice(t, fakeDeviceOpts{})
}

// negotiationFailureServer is a fake device that rejects every offer.
func negotiationFailureServer(t *testing.T) *Client {
	t.Helper()
	_, client := newFakeDevice(t, fakeDeviceOpts{RejectOffer: true})
	return client
}

func TestTakeScreenshotNegotiationFailure(t *testing.T) {
	client := negotiationFailureServer(t)

	if _, err := TakeScreenshot(context.Background(), client); err == nil {
		t.Fatal("expected error when SDP exchange fails")
	}
}

func TestTakeScreenshotContextTimeout(t *testing.T) {
	_, client := answeringVideoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if _, err := TakeScreenshot(ctx, client); err == nil {
		t.Fatal("expected context deadline error when no track arrives")
	}
}

func TestRecordVideoNegotiationFailure(t *testing.T) {
	client := negotiationFailureServer(t)

	outputPath := filepath.Join(t.TempDir(), "out.mp4")
	if err := RecordVideo(context.Background(), client, outputPath, time.Second); err == nil {
		t.Fatal("expected error when SDP exchange fails")
	}
}

func TestRecordVideoContextTimeout(t *testing.T) {
	_, client := answeringVideoServer(t)

	outputPath := filepath.Join(t.TempDir(), "out.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := RecordVideo(ctx, client, outputPath, 5*time.Second); err == nil {
		t.Fatal("expected context deadline error when no track arrives")
	}
}
