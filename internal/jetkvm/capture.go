package jetkvm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
)

const (
	nalSPS byte = 7
	nalPPS byte = 8
	nalIDR byte = 5
)

// newPeerConnection returns a PeerConnection configured for local-network use (no STUN).
func newPeerConnection() (*webrtc.PeerConnection, error) {
	return webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	})
}

// negotiate adds a recvonly video transceiver, creates an offer, waits for ICE
// gathering to complete, exchanges SDP with the device, and sets the remote answer.
func negotiate(ctx context.Context, pc *webrtc.PeerConnection, client *Client) error {
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		return fmt.Errorf("add transceiver: %w", err)
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("create offer: %w", err)
	}

	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("set local description: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-gatherDone:
	}

	answer, err := client.ExchangeSDP(ctx, pc.LocalDescription())
	if err != nil {
		return err
	}
	if err := pc.SetRemoteDescription(*answer); err != nil {
		return fmt.Errorf("set remote description: %w", err)
	}
	return nil
}

// parseNALUnits splits Annex-B H.264 data into individual NAL unit slices.
// Each returned slice includes its leading 0x00 0x00 0x00 0x01 start code.
func parseNALUnits(annexB []byte) [][]byte {
	var units [][]byte
	start := 0
	for i := 0; i+4 <= len(annexB); i++ {
		if i > 0 && annexB[i] == 0 && annexB[i+1] == 0 && annexB[i+2] == 0 && annexB[i+3] == 1 {
			units = append(units, annexB[start:i])
			start = i
		}
	}
	if start < len(annexB) {
		units = append(units, annexB[start:])
	}
	return units
}

// nalUnitType returns the H.264 NAL unit type from an Annex-B slice.
func nalUnitType(unit []byte) byte {
	if len(unit) < 5 {
		return 0
	}
	return unit[4] & 0x1F
}

// captureKeyframe reads from the video track until a complete IDR keyframe
// (preceded by SPS and PPS parameter sets) is assembled, returned in Annex-B format.
func captureKeyframe(ctx context.Context, track *webrtc.TrackRemote) ([]byte, error) {
	depack := &codecs.H264Packet{}
	var spsData, ppsData []byte

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return nil, fmt.Errorf("read RTP: %w", err)
		}
		data, err := depack.Unmarshal(pkt.Payload)
		if err != nil || len(data) == 0 {
			continue
		}
		for _, nal := range parseNALUnits(data) {
			switch nalUnitType(nal) {
			case nalSPS:
				spsData = append([]byte(nil), nal...)
			case nalPPS:
				ppsData = append([]byte(nil), nal...)
			case nalIDR:
				if spsData == nil || ppsData == nil {
					continue
				}
				var frame bytes.Buffer
				frame.Write(spsData)
				frame.Write(ppsData)
				frame.Write(nal)
				return frame.Bytes(), nil
			}
		}
	}
}

// h264FrameToPNG decodes a single H.264 keyframe (Annex-B) to PNG via ffmpeg.
func h264FrameToPNG(ctx context.Context, h264 []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-f", "h264", "-i", "pipe:0",
		"-frames:v", "1",
		"-f", "image2", "-vcodec", "png",
		"pipe:1",
		"-loglevel", "quiet",
	)
	cmd.Stdin = bytes.NewReader(h264)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg PNG decode: %w", err)
	}
	return out, nil
}

// TakeScreenshot establishes a WebRTC session with the JetKVM device, captures
// the first complete video keyframe, and returns it as PNG-encoded bytes.
func TakeScreenshot(ctx context.Context, client *Client) ([]byte, error) {
	pc, err := newPeerConnection()
	if err != nil {
		return nil, err
	}
	defer pc.Close()

	type result struct {
		h264 []byte
		err  error
	}
	ch := make(chan result, 1)

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			return
		}
		go func() {
			h264, err := captureKeyframe(ctx, track)
			select {
			case ch <- result{h264, err}:
			default:
			}
		}()
	})

	if err := negotiate(ctx, pc, client); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return h264FrameToPNG(ctx, r.h264)
	}
}

// RecordVideo establishes a WebRTC session with the JetKVM device and writes
// a video clip of the given duration to outputPath as a fragmented MP4.
func RecordVideo(ctx context.Context, client *Client, outputPath string, duration time.Duration) error {
	pc, err := newPeerConnection()
	if err != nil {
		return err
	}
	defer pc.Close()

	pr, pw := io.Pipe()
	ffmpegErr := make(chan error, 1)
	go func() {
		cmd := exec.CommandContext(ctx,
			"ffmpeg",
			"-f", "h264", "-i", "pipe:0",
			"-c:v", "copy",
			"-movflags", "frag_keyframe+empty_moov",
			"-y", outputPath,
			"-loglevel", "quiet",
		)
		cmd.Stdin = pr
		ffmpegErr <- cmd.Run()
	}()

	trackReady := make(chan *webrtc.TrackRemote, 1)
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			select {
			case trackReady <- track:
			default:
			}
		}
	})

	if err := negotiate(ctx, pc, client); err != nil {
		pw.CloseWithError(err)
		<-ffmpegErr
		return err
	}

	var track *webrtc.TrackRemote
	select {
	case <-ctx.Done():
		pw.CloseWithError(ctx.Err())
		<-ffmpegErr
		return ctx.Err()
	case track = <-trackReady:
	}

	depack := &codecs.H264Packet{}
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			break
		}
		pkt, _, err := track.ReadRTP()
		if err != nil {
			break
		}
		data, err := depack.Unmarshal(pkt.Payload)
		if err != nil || len(data) == 0 {
			continue
		}
		if _, err := pw.Write(data); err != nil {
			break
		}
	}
	pw.Close()
	return <-ffmpegErr
}
