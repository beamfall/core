package integrationhost

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/store"
)

// libraryIDOf adapts a wire string into a store.LibraryID for the request-ingest
// method (the only request field that is a typed store id).
func libraryIDOf(s string) store.LibraryID { return store.LibraryID(s) }

// The Integration Host API wire protocol is length-prefixed JSON frames over the
// unix-domain socket: a 4-byte big-endian length, then that many bytes of JSON.
// The first frame on a connection is the capability-token handshake; every later
// frame is a Host API request. Keeping the framing explicit (not stdio) means a
// worker's own stdout/stderr stay free for logging without corrupting control.

const maxFrameBytes = 1 << 20 // 1 MiB per frame: requests are small control messages

// ErrActionTimeout reports that the worker did not complete an action before
// the Integration Host's own deadline.
var ErrActionTimeout = errors.New("integrationhost: worker action timed out")

// hostRequest is one Host API call from the worker. Method names the operation;
// the remaining fields are method-specific (unused fields are ignored).
type hostRequest struct {
	Method    string              `json:"method"`
	RequestID string              `json:"requestID,omitempty"`
	ActionID  string              `json:"actionID,omitempty"`
	Err       string              `json:"err,omitempty"`
	AfterSeq  int64               `json:"afterSeq,omitempty"`
	Limit     int                 `json:"limit,omitempty"`
	Ref       string              `json:"ref,omitempty"`
	Value     string              `json:"value,omitempty"`
	Kind      string              `json:"kind,omitempty"`
	Payload   json.RawMessage     `json:"payload,omitempty"`
	RunAfter  *time.Time          `json:"runAfter,omitempty"`
	LibraryID string              `json:"libraryID,omitempty"`
	FileMut   FileMutationRequest `json:"fileMutation,omitempty"`
	Outbound  OutboundRequest     `json:"outbound,omitempty"`
	Causation causation.Token     `json:"causationToken,omitempty"`
}

// hostResponse is the supervisor's reply. Err is the permission-denied (or other)
// error string; a non-empty Err means the call failed. Data carries the
// method's success payload.
type hostResponse struct {
	Err  string          `json:"err,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Host API method names (the closed call surface; each maps to one permission).
const (
	methodReadCatalog         = "read-catalog"
	methodRunAction           = "run-action"
	methodActionResult        = "action-result"
	methodSubscribeEvents     = "subscribe-events"
	methodGetSecret           = "get-secret"
	methodSetSecret           = "set-secret"
	methodEnqueueJob          = "enqueue-job"
	methodRequestIngest       = "request-ingest"
	methodRequestFileMutation = "request-file-mutation"
	methodRequestOutbound     = "request-outbound"
	methodWriteStatus         = "write-status"
	methodLinkProfile         = "link-profile"
)

// handleConn runs the per-connection protocol: handshake then request loop. A
// failed handshake closes the connection without serving any method. Each
// connection is bound to the supervisor's single pluginID via s.host.
func (s *Supervisor) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	if !s.negotiateHost(conn) {
		return
	}
	s.actionMu.Lock()
	s.actionConn = conn
	if s.pendingActions == nil {
		s.pendingActions = map[string]chan error{}
	}
	s.actionMu.Unlock()
	defer s.failPendingActions(fmt.Errorf("integrationhost: worker disconnected"))

	for {
		if ctx.Err() != nil {
			return
		}
		raw, err := readFrame(conn)
		if err != nil {
			return // worker disconnected
		}
		var req hostRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = writeResponse(conn, hostResponse{Err: "malformed request"})
			continue
		}
		if req.Method == methodActionResult {
			s.completeAction(req.RequestID, req.Err)
			continue
		}
		resp := s.dispatch(ctx, req)
		s.writeMu.Lock()
		err = writeResponse(conn, resp)
		s.writeMu.Unlock()
		if err != nil {
			return
		}
	}
}

// RunAction sends a descriptor action request to the connected worker and waits
// for its action-result frame. The graph layer already validated descriptor
// identity and approved grants; this method is only the worker transport.
func (s *Supervisor) RunAction(ctx context.Context, pluginID, actionID string) error {
	if pluginID != s.pluginID {
		return fmt.Errorf("integrationhost: action plugin mismatch")
	}
	if !s.generation.Current() {
		return ErrWorkerGenerationRetired
	}
	requestID, err := mintToken()
	if err != nil {
		return err
	}
	ch := make(chan error, 1)
	s.actionMu.Lock()
	conn := s.actionConn
	if conn == nil {
		s.actionMu.Unlock()
		return fmt.Errorf("integrationhost: worker unavailable")
	}
	if s.pendingActions == nil {
		s.pendingActions = map[string]chan error{}
	}
	s.pendingActions[requestID] = ch
	s.actionMu.Unlock()

	causationToken, err := s.host.causationTokenFor("action:"+requestID, s.host.now().UTC())
	if err != nil {
		s.dropAction(requestID)
		return err
	}
	payload, err := json.Marshal(hostRequest{
		Method: methodRunAction, RequestID: requestID, ActionID: actionID, Causation: causationToken,
	})
	if err != nil {
		s.dropAction(requestID)
		return err
	}
	s.writeMu.Lock()
	err = writeFrame(conn, payload)
	s.writeMu.Unlock()
	if err != nil {
		s.dropAction(requestID)
		return err
	}

	timer := time.NewTimer(s.configuredActionTimeout())
	defer timer.Stop()
	select {
	case err := <-ch:
		if !s.generation.Current() {
			return ErrWorkerGenerationRetired
		}
		return err
	case <-ctx.Done():
		s.dropAction(requestID)
		return ctx.Err()
	case <-timer.C:
		s.dropAction(requestID)
		return ErrActionTimeout
	}
}

func (s *Supervisor) completeAction(requestID, errText string) {
	if requestID == "" {
		return
	}
	var err error
	if errText != "" {
		err = errors.New(errText)
	}
	s.actionMu.Lock()
	ch := s.pendingActions[requestID]
	delete(s.pendingActions, requestID)
	s.actionMu.Unlock()
	if ch != nil {
		ch <- err
	}
}

func (s *Supervisor) dropAction(requestID string) {
	s.actionMu.Lock()
	delete(s.pendingActions, requestID)
	s.actionMu.Unlock()
}

func (s *Supervisor) failPendingActions(err error) {
	s.actionMu.Lock()
	pending := s.pendingActions
	s.pendingActions = nil
	s.actionConn = nil
	s.actionMu.Unlock()
	for _, ch := range pending {
		ch <- err
	}
}

// dispatch routes one request to the matching Host method. Every method enforces
// its permission inside the Host; an ungranted call returns ErrPermissionDenied
// as the response Err. dispatch never panics on an unknown method.
func (s *Supervisor) dispatch(ctx context.Context, req hostRequest) hostResponse {
	var response hostResponse
	if err := s.generation.Guard(func() { response = s.dispatchOwned(ctx, req) }); err != nil {
		return jsonResp(struct{}{}, err)
	}
	return response
}

func (s *Supervisor) dispatchOwned(ctx context.Context, req hostRequest) hostResponse {
	switch req.Method {
	case methodReadCatalog:
		libs, err := s.host.ReadCatalog(ctx)
		return jsonResp(libs, err)
	case methodSubscribeEvents:
		events, err := s.host.SubscribeEvents(ctx, req.AfterSeq, req.Limit)
		return jsonResp(events, err)
	case methodGetSecret:
		v, err := s.host.GetSecret(ctx, req.Ref)
		return jsonResp(v, err)
	case methodSetSecret:
		err := s.host.SetSecret(ctx, req.Ref, req.Value)
		return jsonResp(struct{}{}, err)
	case methodEnqueueJob:
		ctx = causation.WithToken(ctx, req.Causation)
		runAfter := time.Time{}
		if req.RunAfter != nil {
			runAfter = *req.RunAfter
		}
		id, err := s.host.EnqueueJob(ctx, req.Kind, req.Payload, runAfter)
		return jsonResp(id, err)
	case methodRequestIngest:
		ctx = causation.WithToken(ctx, req.Causation)
		err := s.host.RequestIngest(ctx, libraryIDOf(req.LibraryID))
		return jsonResp(struct{}{}, err)
	case methodRequestFileMutation:
		ctx = causation.WithToken(ctx, req.Causation)
		err := s.host.RequestFileMutation(ctx, req.FileMut)
		return jsonResp(struct{}{}, err)
	case methodRequestOutbound:
		ctx = causation.WithToken(ctx, req.Causation)
		resp, err := s.host.RequestOutbound(ctx, req.Outbound)
		return jsonResp(resp, err)
	case methodWriteStatus:
		err := s.host.WriteStatus(ctx, req.Kind, req.Value)
		return jsonResp(struct{}{}, err)
	case methodLinkProfile:
		err := s.host.LinkProfile(ctx, store.ProfileID(req.Ref), req.Value)
		return jsonResp(struct{}{}, err)
	default:
		return hostResponse{Err: "unknown method"}
	}
}

func jsonResp(data any, err error) hostResponse {
	if err != nil {
		return hostResponse{Err: err.Error()}
	}
	raw, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		return hostResponse{Err: "internal: response marshal failed"}
	}
	return hostResponse{Data: raw}
}

func writeResponse(conn net.Conn, resp hostResponse) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		raw, _ = json.Marshal(hostResponse{Err: "internal"})
	}
	return writeFrame(conn, raw)
}

// readFrame reads one length-prefixed JSON frame. A frame larger than
// maxFrameBytes is rejected (a worker cannot exhaust server memory by claiming a
// huge length).
func readFrame(conn net.Conn) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > maxFrameBytes {
		return nil, fmt.Errorf("integrationhost: frame length %d out of range", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeFrame(conn net.Conn, payload []byte) error {
	if len(payload) > maxFrameBytes {
		return fmt.Errorf("integrationhost: frame too large (%d)", len(payload))
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}
