package messagehub

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"

	"com.lwc.message_center_server/internal/logger"
)

const MAX_PAYLOAD_SIZE = 1 << 20

type RegMessage struct {
	Ctx  context.Context
	Conn net.Conn
}

type frame struct {
	err *frameErr

	fin     bool
	opCode  byte
	payload []byte
}

type MessageHub struct {
	regChan chan *RegMessage
}

func NewMessageHug() *MessageHub {
	return &MessageHub{
		regChan: make(chan *RegMessage),
	}
}

func (mh *MessageHub) Start() error {
	for true {
		msg := <-mh.regChan

		frameChan := make(chan *frame)
		outChan := make(chan *frame)
		go mh.readFrame(msg.Ctx, msg.Conn, frameChan)
		go mh.processFrame(msg.Ctx, msg.Conn, frameChan, outChan)
		go mh.sendFrame(msg.Ctx, msg.Conn, outChan)
	}
	return nil
}

func (mh *MessageHub) RegChan() chan *RegMessage {
	return mh.regChan
}

func (mg *MessageHub) readFrame(ctx context.Context, conn io.Reader, frameChan chan *frame) {
	l := logger.Get(ctx)
	br := bufio.NewReader(conn)

	for true {
		var hdrBytes [2]byte
		l.Debug("Frame Reader: Reading a frame...")
		i, err := io.ReadFull(br, hdrBytes[:])
		l.Debug("Frame Reader: Got something", "i", i, "readBytes", logger.GetPPBytesStr(hdrBytes[:]))
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				l.Debug("Connection closed, retuning...")
				return
			}
			l.Debug(fmt.Sprintf("Unexpected error %s, returning...", err.Error()))
			break
		}

		if i != 2 {
			frameChan <- &frame{
				err: &frameErr{Message: "Insufficient byte read when reading hdr bytes, possible unexpected EOF", CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}

		hdr1, hdr2 := hdrBytes[0], hdrBytes[1]

		l.Debug("Frame Reader: header bytes", "hdr1", logger.GetPPByteStr(hdr1), "hdr2", logger.GetPPByteStr(hdr2))

		// hdr1 bit layout
		//   0,             000,    0000
		// fin, extension (rsv), op code

		// fin: determine whether fragmentations happen
		fin := (hdr1 & 0x80) != 0

		// rsv: any negotiated extensions?
		rsv := (hdr1 & 0x70)
		if rsv != 0 {
			frameChan <- &frame{
				err: &frameErr{Message: "rsv is not supported", CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION},
			}
			break
		}

		// opCode: opCode code
		opCode := (hdr1 & 0x0F)
		if (opCode == 0x8 || opCode == 0x9 || opCode == 0xA) && !fin {
			frameChan <- &frame{
				err: &frameErr{Message: "Control frame cannot be fragmented", CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION},
			}
			break
		}

		l.Debug("Frame Reader: header1 is valid", "fin", fin, "rsv", logger.GetPPByteStr(rsv), "op", logger.GetPPByteStr(opCode))

		// hdr2 bit layout
		//    0,     0000000
		// mask, length hint

		masked := (hdr2 & 0x80) != 0
		if !masked {
			l.Error("Payload must be masked")
			frameChan <- &frame{
				err: &frameErr{Message: "Payload must be masked", CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION},
			}
			break
		}
		plenb := (hdr2 & 0x7F)
		l.Debug("Frame Reader: header2 is valid", "mask", masked, "plen7", logger.GetPPByteStr(plenb))

		var plen uint64

		// The length rule:
		// If it is 0 - 125, thats the actual length
		// If it is 126, the length is in the following 2 bytes
		// If it is 127, the length is in the following 8 bytes
		switch plenb {
		case 126:
			l.Debug("Frame Reader: plen is in the following 2 bytes")
			var lenBytes [2]byte
			i, err := io.ReadFull(br, lenBytes[:])
			if err != nil {
				frameChan <- &frame{
					err: &frameErr{Message: fmt.Sprintf("Unexpected Error While Reading Payload Length: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
				}
				break
			}
			if i != 2 {
				frameChan <- &frame{
					err: &frameErr{Message: "Insufficient byte read when reading length bytes, possible unexpected EOF", CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
				}
				break
			}

			plen = uint64(binary.BigEndian.Uint16(lenBytes[:]))
		case 127:
			l.Debug("Frame Reader: plen is in the following 8 bytes")
			var lenBytes [8]byte
			i, err := io.ReadFull(br, lenBytes[:])
			if err != nil {
				frameChan <- &frame{
					err: &frameErr{Message: fmt.Sprintf("Unexpected Error While Reading Payload Length: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
				}
				break
			}
			if i != 8 {
				frameChan <- &frame{
					err: &frameErr{Message: "Insufficient byte read when reading length bytes, possible unexpected EOF", CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
				}
				break
			}

			plen = binary.BigEndian.Uint64(lenBytes[:])
		default:
			l.Debug("Frame Reader: plen is the plen")
			plen = uint64(plenb)
		}

		l.Debug(fmt.Sprintf("Final plen: %d", plen))

		if plen > MAX_PAYLOAD_SIZE {
			frameChan <- &frame{
				err: &frameErr{Message: "Payload size limit exceeded", CloseCode: CLOSE_CODE_PAYLOAD_TOO_BIG},
			}
			break
		}

		var maskKey [4]byte
		i, err = io.ReadFull(br, maskKey[:])
		if err != nil {
			frameChan <- &frame{
				err: &frameErr{Message: fmt.Sprintf("Unexpected Error While Reading mask key: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}

		if i != 4 {
			frameChan <- &frame{
				err: &frameErr{Message: "Insufficient byte read when reading mask key, possible unexpected EOF", CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}
		l.Debug(fmt.Sprintf("mask key: %s", logger.GetPPBytesStr(maskKey[:])))

		payload := make([]byte, plen)
		i, err = io.ReadFull(br, payload)
		if err != nil {
			frameChan <- &frame{
				err: &frameErr{Message: fmt.Sprintf("Unexpected Error While Reading Payload: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}

		// this cast will be safe, since we checked above
		if i != int(plen) {
			frameChan <- &frame{
				err: &frameErr{Message: "Insufficient byte read when reading payload, possible unexpected EOF", CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}
		l.Debug(fmt.Sprintf("masked payload: %s", logger.GetPPBytesStr(payload)))

		for i := uint64(0); i < plen; i++ {
			payload[i] ^= maskKey[i%4]
		}
		l.Debug(fmt.Sprintf("unmasked payload: %s", logger.GetPPBytesStr(payload)))

		f := &frame{
			fin:     fin,
			opCode:  opCode,
			payload: payload[:],
		}

		// select
		frameChan <- f
	}
}

func (mg *MessageHub) processFrame(ctx context.Context, conn net.Conn, frameChan chan *frame, outChan chan *frame) {
	fragmentations := make([]byte, 0)
	var rootFrame *frame
	l := logger.Get(ctx)
	for true {
		l.Debug("Frame Processor: waiting for the world to change~")
		f := <-frameChan
		l.Debug("Frame Processor: got something")

		if f.err != nil {
			l.Debug("Frame Processor: processing error frame, sending error frame, and close", "error", f.err.Error())
			code := f.err.CloseCode
			highByte := byte(code >> 8)
			lowByte := byte(code)
			payload := []byte{highByte, lowByte}

			payload = append(payload, []byte(f.err.Message)...)

			// select
			outChan <- &frame{
				opCode:  OP_CODE_CLOSE,
				fin:     true,
				payload: payload,
			}
			break
		}

		if rootFrame != nil && f.opCode != OP_CODE_CONTINUATION {
			l.Debug("Frame Processor: unexpected non continuation frame when a fragmented frame is expected", "opcode", f.opCode)
			code := CLOSE_CODE_PROTOCOL_VIOLATION
			highByte := byte(code >> 8)
			lowByte := byte(code)
			payload := []byte{highByte, lowByte}

			// select
			outChan <- &frame{
				opCode:  OP_CODE_CLOSE,
				fin:     true,
				payload: payload,
			}
			break
		}

		// handle special op code
		if f.opCode == OP_CODE_CLOSE {
			l.Debug("Frame Processor: client want to close, we will do so")
			outChan <- &frame{
				opCode: OP_CODE_CLOSE,
				fin:    true,
			}
			break
		}

		if f.opCode == OP_CODE_PING {
			l.Debug("Frame Processor: client is a loser, and want to play ping pong with a server, we will do so")
			outChan <- &frame{
				opCode:  OP_CODE_PONG,
				fin:     true,
				payload: f.payload,
			}
			continue
		}

		if f.opCode == OP_CODE_PONG {
			l.Debug("Frame Processor: PONG")
			// nothing to do for now, update connection liveliness
			continue
		}

		if !f.fin {
			l.Debug("Frame Processor: Fragmented frame detected")
			if rootFrame == nil && f.opCode != OP_CODE_BINARY && f.opCode != OP_CODE_TEXT {
				l.Debug("Frame Processor: First Fragmented frame detected, but opCode is not binary or text", "opCode", f.opCode)
				code := CLOSE_CODE_PROTOCOL_VIOLATION
				highByte := byte(code >> 8)
				lowByte := byte(code)
				payload := []byte{highByte, lowByte}

				// select
				outChan <- &frame{
					opCode:  OP_CODE_CLOSE,
					fin:     true,
					payload: payload,
				}
				break
			}
			if rootFrame != nil {
				l.Debug("Frame Processor: this is the first fragmented frame", "opCode", f.opCode)
				rootFrame = f
			}

			l.Debug("Frame Processor: payload of this fragmented frame", "payload", string(f.payload))
			fragmentations = append(fragmentations, f.payload...)
			continue
		}

		finalPayload := append(fragmentations, f.payload...)
		l.Debug("Frame Processor: all fragmented frame arrived, echoing back to client", "allPayload", string(finalPayload))

		outFrame := &frame{
			payload: finalPayload,
		}
		if rootFrame != nil {
			outFrame.opCode = rootFrame.opCode
		} else {
			outFrame.opCode = f.opCode
		}

		outChan <- outFrame
		rootFrame = nil
		fragmentations = make([]byte, 0)
	}
}

func (mg *MessageHub) sendFrame(ctx context.Context, conn net.Conn, outChan chan *frame) {
	bw := bufio.NewWriter(conn)
	l := logger.Get(ctx)

	for true {
		l.Debug("Frame Sender: i am not john mayer i will not sing waiting on the world to change")
		outFrame := <-outChan
		payload, opCode := outFrame.payload, outFrame.opCode
		l.Debug("Frame Sender: got something", "opCode", opCode, "payload", string(payload))

		payloadSize := len(payload)
		start, end := 0, min(MAX_PAYLOAD_SIZE, payloadSize-1)+1
		inFrag := payloadSize > end

		for true {
			l.Debug(fmt.Sprintf("Frame Sender: sending frame, is this frame fragmented? %t", inFrag), "start", start, "end", end, "payloadSize", payloadSize)
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

			l.Debug(fmt.Sprintf("Frame Sender: first header byte %s", logger.GetPPByteStr(hdr1)))

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
			l.Debug(fmt.Sprintf("Frame Sender: header bytes + len bytes: %s", logger.GetPPBytesStr(frameBytes)), "payload", string(payload[start:end]))

			frameBytes = append(frameBytes, payload[start:end]...)
			l.Debug("Frame Sender: sending the frame")
			i, err := bw.Write(frameBytes)
			if err != nil {
				l.Error(fmt.Sprintf("Unexpected error when writing res frame: %s, breaking", err.Error()))
				break
			}
			if i != len(frameBytes) {
				l.Error("Frame is not fully written due to unexpected reasons, breaking", "i", i, "frameLen", len(frameBytes))
				break

			}

			err = bw.Flush()
			if err != nil {
				l.Error(fmt.Sprintf("Unexpected error when flushing bufio writer: %s, breaking", err.Error()))
				break
			}

			// if inFrag, update start end and inFrag, continue
			if !inFrag {
				l.Debug("Frame Sender: not fragmented, breaking frame sending loop")
				break
			}
			l.Debug("Frame Sender: fragmented, setting up next frame")

			start, end = end, min(end+MAX_PAYLOAD_SIZE, payloadSize-1)+1
			inFrag = payloadSize > end
		}
	}
}
