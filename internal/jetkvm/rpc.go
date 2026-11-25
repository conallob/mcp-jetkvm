package jetkvm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v3"
)

// rpcChannelLabel is the WebRTC data channel label JetKVM uses for its
// bidirectional JSON-RPC 2.0 control channel.
const rpcChannelLabel = "rpc"

// jsonrpcRequest is the wire format for outgoing JSON-RPC 2.0 requests.
type jsonrpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
	ID      int64          `json:"id"`
}

// jsonrpcResponse is the wire format for incoming JSON-RPC 2.0 responses.
// JetKVM also sends unsolicited event messages over the same channel (a
// "method" field with no "id"); those are ignored by decodeRPCMessage.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	ID      *int64          `json:"id,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *jsonrpcError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

// decodeRPCMessage parses a data channel message and reports whether it is a
// response to a request (as opposed to an unsolicited event).
func decodeRPCMessage(data []byte) (*jsonrpcResponse, bool) {
	var resp jsonrpcResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, false
	}
	if resp.ID == nil {
		return nil, false
	}
	return &resp, true
}

// rpcSession is an open WebRTC data channel session used to issue JSON-RPC
// 2.0 calls to a JetKVM device's control channel.
type rpcSession struct {
	pc      *webrtc.PeerConnection
	dc      *webrtc.DataChannel
	nextID  int64
	mu      sync.Mutex
	pending map[int64]chan *jsonrpcResponse
}

// openRPCSession establishes a WebRTC session with the device and opens the
// "rpc" data channel, waiting for it to become ready for use.
func openRPCSession(ctx context.Context, client *Client) (*rpcSession, error) {
	pc, err := newPeerConnection()
	if err != nil {
		return nil, err
	}

	ordered := true
	dc, err := pc.CreateDataChannel(rpcChannelLabel, &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("create RPC data channel: %w", err)
	}

	s := &rpcSession{
		pc:      pc,
		dc:      dc,
		pending: make(map[int64]chan *jsonrpcResponse),
	}

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		resp, ok := decodeRPCMessage(msg.Data)
		if !ok {
			return
		}
		s.mu.Lock()
		ch, found := s.pending[*resp.ID]
		if found {
			delete(s.pending, *resp.ID)
		}
		s.mu.Unlock()
		if found {
			ch <- resp
		}
	})

	opened := make(chan struct{})
	dc.OnOpen(func() { close(opened) })

	if err := completeNegotiation(ctx, pc, client); err != nil {
		pc.Close()
		return nil, err
	}

	select {
	case <-ctx.Done():
		pc.Close()
		return nil, ctx.Err()
	case <-opened:
	}

	return s, nil
}

// Call issues a JSON-RPC request over the data channel and waits for the
// matching response, decoding its result into out (if non-nil).
func (s *rpcSession) Call(ctx context.Context, method string, params map[string]any, out any) error {
	id := atomic.AddInt64(&s.nextID, 1)
	respCh := make(chan *jsonrpcResponse, 1)

	s.mu.Lock()
	s.pending[id] = respCh
	s.mu.Unlock()

	req := jsonrpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: id}
	raw, err := json.Marshal(req)
	if err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return fmt.Errorf("marshal RPC request: %w", err)
	}

	if err := s.dc.SendText(string(raw)); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return fmt.Errorf("send RPC request: %w", err)
	}

	select {
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return ctx.Err()
	case resp := <-respCh:
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("unmarshal RPC result: %w", err)
			}
		}
		return nil
	}
}

// Close tears down the RPC session's WebRTC connection.
func (s *rpcSession) Close() error {
	return s.pc.Close()
}

// callRPC is a convenience wrapper that opens an RPC session, issues a
// single call, and closes the session again.
func callRPC(ctx context.Context, client *Client, method string, params map[string]any, out any) error {
	s, err := openRPCSession(ctx, client)
	if err != nil {
		return fmt.Errorf("open RPC session: %w", err)
	}
	defer func() { _ = s.Close() }()

	callCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}

	return s.Call(callCtx, method, params, out)
}
