package websocket

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync/atomic"
	"time"

	"com.lwc.message_center_server/internal/assert"
	"com.lwc.message_center_server/internal/logger"
)

type inFrame struct {
	Fin     bool
	OpCode  byte
	Payload []byte
}

type iOutFrame interface {
	GetOpCode() byte
	GetPayload() []byte
}

type outFrame struct {
	OpCode  byte
	Payload []byte
}

func (of outFrame) GetOpCode() byte {
	return of.OpCode
}

func (of outFrame) GetPayload() []byte {
	return of.Payload
}

type TextFrame struct {
	Payload []byte
}

func (tf TextFrame) GetOpCode() byte {
	return OP_CODE_TEXT
}

func (tf TextFrame) GetPayload() []byte {
	return tf.Payload
}

// Some notes on error handling / close handling, applied to who do the logging as well
// - If the error is connection level, like closed connection immediate return, call cancelFunc, populate the cancel signal
// - If the error is protocol level, like invalid frame, internal error, send frame to sendChan, let a error frame res be sent, and let send() initiate the cancel signal
type WebSocket struct {
	// support net.conn, and make the package easier to test
	conn            net.Conn
	ctx             context.Context
	cancelFunc      context.CancelFunc
	readChan        chan []byte
	sendChan        chan iOutFrame
	readSetupDone   atomic.Bool
	sendSetupDone   atomic.Bool
	readClosed      atomic.Bool
	sendClosed      atomic.Bool
	l               *slog.Logger
	br              *bufio.Reader
	bw              *bufio.Writer
	allowedIdleTime time.Duration
	pingInterval    time.Duration
}

func NewWebSocket(ctx context.Context, conn net.Conn, allowedIdleTime time.Duration, pingInterval time.Duration) *WebSocket {
	ctxWCan, cancelFunc := context.WithCancel(ctx)
	return &WebSocket{
		ctx:             ctxWCan,
		conn:            conn,
		cancelFunc:      cancelFunc,
		readChan:        make(chan []byte, 10),
		sendChan:        make(chan iOutFrame, 10),
		l:               logger.Get(ctx),
		br:              bufio.NewReader(conn),
		bw:              bufio.NewWriter(conn),
		allowedIdleTime: allowedIdleTime,
		pingInterval:    pingInterval,
	}
}

func (ws *WebSocket) Setup() {
	assert.Assert(!ws.readSetupDone.Load(), "Setup has been called before")
	assert.Assert(!ws.sendSetupDone.Load(), "Setup has been called before")
	go ws.send()
	go ws.read()

	ws.conn.SetReadDeadline(time.Now().Add(ws.allowedIdleTime))
	if ws.pingInterval > 0 {
		go ws.ping()
	}

	ws.readSetupDone.Store(true)
	ws.sendSetupDone.Store(true)
}

func (ws *WebSocket) Read() ([]byte, error) {
	assert.Assert(ws.readSetupDone.Load(), "WebSocket.Setup has not bee called, Read has not been setup")

	if ws.readClosed.Load() {
		return nil, errors.Join(WebSocketClosed, ws.ctx.Err())
	}
	select {
	case bs := <-ws.readChan:
		return bs, nil
	case <-ws.ctx.Done():
		return nil, errors.Join(WebSocketClosed, ws.ctx.Err())
	}
}

func (ws *WebSocket) Send(f iOutFrame) error {
	assert.Assert(ws.sendSetupDone.Load(), "WebSocket.Setup has not bee called, send has not been setup")

	if ws.sendClosed.Load() {
		return WebSocketClosed
	}

	select {
	case ws.sendChan <- f:
		return nil
	case <-ws.ctx.Done():
		close(ws.sendChan)
		ws.sendClosed.Store(true)
		return errors.Join(WebSocketClosed, ws.ctx.Err())
	}
}

func (ws *WebSocket) ping() {
	pingFrame := &outFrame{
		OpCode: OP_CODE_PING,
	}

	for true {
		if ws.sendClosed.Load() || ws.readClosed.Load() {
			ws.l.Debug("Ping-er: send / read closed, returning", "sendClosed", ws.sendClosed.Load(), "readClosed", ws.readClosed.Load())
			return
		}

		select {
		case <-time.Tick(ws.pingInterval):
			ws.l.Debug("Ping-er: PING PONG TIME")
			ws.Send(pingFrame)
		case <-ws.ctx.Done():
			ws.l.Debug("Ping-er: context done")
			return
		}
	}
}

func (ws *WebSocket) read() {
	assert.AssertNotNil(ws.br, "Buffer Reader is nil, cannot proceed reading")

	fragmentations := make([]byte, 0)
	var rootFrame *inFrame

Outer:
	for true {
		ws.l.Debug("Frame Reader: in loop")
		f, err := ws.readFrame()
		if err != nil {
			var fe *frameErr
			if errors.As(err, &fe) {
				ws.l.Error("Frame Reader: protocol error found during frame reading", "err", err)
				ws.Send(&outFrame{
					OpCode:  OP_CODE_CLOSE,
					Payload: fe.ToPayload(),
				})
				break
			}
			if errors.Is(err, WebSocketClosed) {
				ws.l.Error("Frame Reader: web socket closed during frame reading", "err", err)
				break
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				ws.l.Error("Frame Reader: read deadline exceeded", "err", err)
				break
			}
			ws.l.Error("Frame Reader: unknown and unhandled error, failing fast and closing connection", "err", err)
			break
		}

		if rootFrame != nil && f.OpCode != OP_CODE_CONTINUATION {
			fe := &frameErr{
				Message:   fmt.Sprintf("Unexpected non continuation frame when a fragmented frame is expected, opCode provided: %s", logger.GetPPByteStr(f.OpCode)),
				CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION,
			}
			ws.l.Error("Frame Reader: error while processing frame", "err", fe)
			ws.Send(&outFrame{
				OpCode:  OP_CODE_CLOSE,
				Payload: fe.ToPayload(),
			})
			break
		}

		if f.OpCode == OP_CODE_CLOSE {
			ws.l.Debug("Frame Reader: client want to close, we will do so")
			ws.Send(&outFrame{
				OpCode: OP_CODE_CLOSE,
			})
			break
		}

		if f.OpCode == OP_CODE_PING {
			ws.l.Debug("Frame Reader: client is a loser, and want to play ping pong with a server, we will do so")
			ws.Send(&outFrame{
				OpCode:  OP_CODE_PONG,
				Payload: f.Payload,
			})
			continue
		}

		if f.OpCode == OP_CODE_PONG {
			ws.l.Debug("Frame Reader: PONG")
			continue
		}

		if f.OpCode == OP_CODE_BINARY {
			fe := &frameErr{
				Message:   "Binary format payload is not supported",
				CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION,
			}
			ws.l.Error("Frame Reader: error while processing frame", "err", fe)

			ws.Send(&outFrame{
				OpCode:  OP_CODE_CLOSE,
				Payload: fe.ToPayload(),
			})
			break
		}

		if !f.Fin {
			ws.l.Debug("Frame Reader: Fragmented frame detected")
			if rootFrame == nil && f.OpCode != OP_CODE_TEXT {
				fe := &frameErr{
					Message:   fmt.Sprintf("First Fragmented frame detected, but opCode is not binary or text, opCode provided: %s", logger.GetPPByteStr(f.OpCode)),
					CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION,
				}
				ws.l.Error("Frame Reader: error while processing frame", "err", fe)

				ws.Send(&outFrame{
					OpCode:  OP_CODE_CLOSE,
					Payload: fe.ToPayload(),
				})
				break
			}
			if rootFrame == nil {
				ws.l.Debug("Frame Reader: this is the first fragmented frame", "opCode", f.OpCode)
				rootFrame = f
			}

			ws.l.Debug("Frame Reader: payload of this fragmented frame", "payload", string(f.Payload))
			fragmentations = append(fragmentations, f.Payload...)
			continue
		}

		finalPayload := append(fragmentations, f.Payload...)
		ws.l.Debug("Frame Reader: all fragmented frame arrived, echoing back to client", "allPayload", string(finalPayload))

		select {
		case ws.readChan <- finalPayload:
			rootFrame = nil
			fragmentations = make([]byte, 0)
		case <-ws.ctx.Done():
			break Outer
		}
	}

	ws.cancelFunc()
	close(ws.readChan)
	ws.readClosed.Store(true)
}

func (ws *WebSocket) readFrame() (*inFrame, error) {
	var hdrBytes [2]byte
	ws.l.Debug("Frame Reader: Reading a frame...")

	// see setup, deadline is set for the underlying conn
	// and also maintained periodic by ping()
	// io.ReadFull will simply return err when deadline met
	i, err := io.ReadFull(ws.br, hdrBytes[:])
	ws.l.Debug("Frame Reader: Got something", "i", i, "readBytes", logger.GetPPBytesStr(hdrBytes[:]), "err", err)
	if err != nil {
		return nil, errors.Join(err, WebSocketClosed)
	}

	if i != 2 {
		return nil, &frameErr{
			Message:   "Insufficient byte read when reading hdr bytes, possible unexpected EOF",
			CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
		}
	}

	ws.conn.SetDeadline(time.Now().Add(ws.allowedIdleTime))
	hdr1, hdr2 := hdrBytes[0], hdrBytes[1]

	ws.l.Debug("Frame Reader: header bytes", "hdr1", logger.GetPPByteStr(hdr1), "hdr2", logger.GetPPByteStr(hdr2))

	// hdr1 bit layout
	//   0,             000,    0000
	// fin, extension (rsv), op code

	// fin: determine whether fragmentations happen
	fin := (hdr1 & 0x80) != 0

	// rsv: any negotiated extensions?
	rsv := (hdr1 & 0x70)
	if rsv != 0 {
		return nil, &frameErr{
			Message:   "rsv is not supported",
			CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION,
		}
	}

	// opCode: opCode code
	opCode := (hdr1 & 0x0F)
	if (opCode == 0x8 || opCode == 0x9 || opCode == 0xA) && !fin {
		return nil, &frameErr{
			Message:   "Control frame cannot be fragmented",
			CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION,
		}
	}

	ws.l.Debug("Frame Reader: header1 is valid", "fin", fin, "rsv", logger.GetPPByteStr(rsv), "op", logger.GetPPByteStr(opCode))

	// hdr2 bit layout
	//    0,     0000000
	// mask, length hint

	masked := (hdr2 & 0x80) != 0
	if !masked {
		return nil, &frameErr{
			Message:   "Payload must be masked",
			CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION,
		}
	}
	plenb := (hdr2 & 0x7F)
	ws.l.Debug("Frame Reader: header2 is valid", "mask", masked, "plen7", logger.GetPPByteStr(plenb))

	var plen uint64

	// The length rule:
	// If it is 0 - 125, thats the actual length
	// If it is 126, the length is in the following 2 bytes
	// If it is 127, the length is in the following 8 bytes
	switch plenb {
	case 126:
		ws.l.Debug("Frame Reader: plen is in the following 2 bytes")
		var lenBytes [2]byte
		i, err := io.ReadFull(ws.br, lenBytes[:])
		if err != nil {
			return nil, errors.Join(
				&frameErr{
					Message:   "Unexpected Error While Reading Payload Length",
					CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
				},
				err,
			)
		}
		if i != 2 {
			return nil, &frameErr{
				Message:   "Insufficient byte read when reading length bytes, possible unexpected EOF",
				CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
			}
		}

		plen = uint64(binary.BigEndian.Uint16(lenBytes[:]))
	case 127:
		ws.l.Debug("Frame Reader: plen is in the following 8 bytes")
		var lenBytes [8]byte
		i, err := io.ReadFull(ws.br, lenBytes[:])
		if err != nil {
			return nil, errors.Join(
				&frameErr{
					Message:   "Unexpected Error While Reading Payload Length",
					CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
				},
				err,
			)
		}
		if i != 8 {
			return nil, &frameErr{
				Message:   "Insufficient byte read when reading length bytes, possible unexpected EOF",
				CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
			}
		}

		plen = binary.BigEndian.Uint64(lenBytes[:])
	default:
		ws.l.Debug("Frame Reader: plen is the plen")
		plen = uint64(plenb)
	}

	ws.l.Debug(fmt.Sprintf("Frame Reader: Final plen: %d", plen))

	if plen > MAX_PAYLOAD_SIZE {
		return nil, &frameErr{
			Message:   "Payload size limit exceeded",
			CloseCode: CLOSE_CODE_PAYLOAD_TOO_BIG,
		}
	}

	var maskKey [4]byte
	i, err = io.ReadFull(ws.br, maskKey[:])
	if err != nil {
		return nil, errors.Join(
			&frameErr{
				Message:   "Unexpected Error While Reading mask key",
				CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
			},
			err,
		)
	}

	if i != 4 {
		return nil, &frameErr{
			Message:   "Insufficient byte read when reading mask key, possible unexpected EOF",
			CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
		}
	}
	ws.l.Debug(fmt.Sprintf("Frame Reader: mask key: %s", logger.GetPPBytesStr(maskKey[:])))

	payload := make([]byte, plen)
	i, err = io.ReadFull(ws.br, payload)
	if err != nil {
		return nil, &frameErr{
			Message:   fmt.Sprintf("Unexpected Error While Reading Payload: %s", err.Error()),
			CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
		}
	}

	// this cast will be safe, since we checked above
	if i != int(plen) {
		return nil, &frameErr{
			Message:   "Insufficient byte read when reading payload, possible unexpected EOF",
			CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR,
		}
	}
	ws.l.Debug(fmt.Sprintf("Frame Reader: masked payload: %s", logger.GetPPBytesStr(payload)))

	for i := uint64(0); i < plen; i++ {
		payload[i] ^= maskKey[i%4]
	}
	ws.l.Debug(fmt.Sprintf("Frame Reader: unmasked payload: %s", logger.GetPPBytesStr(payload)))

	return &inFrame{
		Fin:     fin,
		OpCode:  opCode,
		Payload: payload,
	}, nil
}

func (ws *WebSocket) send() {
	assert.AssertNotNil(ws.bw, "Buffer writer is nil, cannot proceed with send")

	ws.l.Debug("Frame Sender: waiting patiently for send event")

Outer:
	for true {
		var f iOutFrame
		select {
		case f = <-ws.sendChan:
			if f == nil {
				ws.l.Debug("Frame Sender: Unexpected nil / channel closed, breaking")
				break Outer
			}
		case <-ws.ctx.Done():
			ws.l.Debug("Frame Sender: Context done, breaking")
			break Outer
		}
		payload, opCode := f.GetPayload(), f.GetOpCode()
		ws.l.Debug("Frame Sender: got something", "opCode", opCode, "payload", string(payload))

		payloadSize := len(payload)
		start, end := 0, min(MAX_PAYLOAD_SIZE, payloadSize-1)+1
		inFrag := payloadSize > end

		for true {
			ws.l.Debug(fmt.Sprintf("Frame Sender: sending frame, is this frame fragmented? %t", inFrag), "start", start, "end", end, "payloadSize", payloadSize)
			frameBytes := make([]byte, 0, 10)
			var hdr1 byte

			if !inFrag {
				hdr1 |= 0x80
			}

			// & 0x0F make sure no first 4 bytes gets in from the opCode
			if start == 0 {
				hdr1 |= opCode & 0x0F
			} else {
				hdr1 |= OP_CODE_CONTINUATION & 0x0F
			}

			ws.l.Debug(fmt.Sprintf("Frame Sender: first header byte %s", logger.GetPPByteStr(hdr1)))

			frameBytes = append(frameBytes, hdr1)

			// calculate plen7 and lenByte for start and end
			framePayloadSize := end - start
			switch {
			case framePayloadSize <= 125:
				frameBytes = append(frameBytes, byte(framePayloadSize))
			case framePayloadSize <= 65535:
				frameBytes = append(frameBytes, byte(126))
				var lenBytes [2]byte
				binary.BigEndian.PutUint16(lenBytes[:], uint16(framePayloadSize))
				frameBytes = append(frameBytes, lenBytes[:]...)
			default:
				frameBytes = append(frameBytes, byte(127))
				var lenBytes [8]byte
				binary.BigEndian.PutUint64(lenBytes[:], uint64(framePayloadSize))
				frameBytes = append(frameBytes, lenBytes[:]...)
			}
			ws.l.Debug(fmt.Sprintf("Frame Sender: header bytes + len bytes: %s", logger.GetPPBytesStr(frameBytes)), "payload", string(payload[start:end]))

			frameBytes = append(frameBytes, payload[start:end]...)
			ws.l.Debug("Frame Sender: sending the frame")

			err := ws.conn.SetWriteDeadline(time.Now().Add(ws.allowedIdleTime))
			if err != nil {
				ws.l.Error(fmt.Sprintf("Unexpected error when setting write deadline: %s, breaking", err.Error()))
				break Outer
			}
			i, err := ws.bw.Write(frameBytes)
			if err != nil {
				ws.l.Error(fmt.Sprintf("Unexpected error when writing res frame: %s, breaking", err.Error()))
				break Outer
			}
			if i != len(frameBytes) {
				ws.l.Error("Frame is not fully written due to unexpected reasons, breaking", "i", i, "frameLen", len(frameBytes))
				break Outer

			}

			err = ws.bw.Flush()
			if err != nil {
				ws.l.Error(fmt.Sprintf("Unexpected error when flushing bufio writer: %s, breaking", err.Error()))
				break Outer
			}

			// if inFrag, update start end and inFrag, continue
			if !inFrag {
				ws.l.Debug("Frame Sender: not fragmented, breaking frame sending loop")
				break
			}
			ws.l.Debug("Frame Sender: fragmented, setting up next frame")

			start, end = end, min(end+MAX_PAYLOAD_SIZE, payloadSize-1)+1
			inFrag = payloadSize > end
		}
	}

	ws.cancelFunc()
}
