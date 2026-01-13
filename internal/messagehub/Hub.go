package messagehub

import (
	"bufio"
	"context"
	"encoding/binary"
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
	hdrBytes := make([]byte, 0, 2)

	for true {
		i, err := io.ReadFull(br, hdrBytes[:])
		if err != nil {
			frameChan <- &frame{
				err: &frameErr{Message: fmt.Sprintf("Error when reading hdr bytes: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}

		if i != 2 {
			frameChan <- &frame{
				err: &frameErr{Message: "Insufficient byte read when reading hdr bytes, possible unexpected EOF", CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}

		hdr1, hdr2 := hdrBytes[0], hdrBytes[1]

		l.Debug("header bytes", "hdr1", logger.GetPPByteStr(hdr1), "hdr2", logger.GetPPByteStr(hdr2))

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
		l.Debug("header1", "fin", fin, "rsv", logger.GetPPByteStr(rsv), "op", logger.GetPPByteStr(opCode))
		if (opCode == 0x8 || opCode == 0x9 || opCode == 0xA) && !fin {
			frameChan <- &frame{
				err: &frameErr{Message: "Control frame cannot be fragmented", CloseCode: CLOSE_CODE_PROTOCOL_VIOLATION},
			}
			break
		}

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
		l.Debug("header2", "mask", masked, "plen7", logger.GetPPByteStr(plenb))

		var plen uint64

		// The length rule:
		// If it is 0 - 125, thats the actual length
		// If it is 126, the length is in the following 2 bytes
		// If it is 127, the length is in the following 8 bytes
		switch plenb {
		case 126:
			lenBytes := make([]byte, 0, 2)
			i, err := io.ReadFull(br, lenBytes[:])
			if err != nil {
				frameChan <- &frame{
					err: &frameErr{Message: fmt.Sprintf("Unexpected Error While Reading Payload Length: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
				}
				break
			}
			if i != 2 {
				frameChan <- &frame{
					err: &frameErr{Message: fmt.Sprintf("Insufficient byte read when reading length bytes, possible unexpected EOF: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
				}
				break
			}

			plen = uint64(binary.BigEndian.Uint16(lenBytes))
		case 127:
			lenBytes := make([]byte, 0, 8)
			i, err := io.ReadFull(br, lenBytes[:])
			if err != nil {
				frameChan <- &frame{
					err: &frameErr{Message: fmt.Sprintf("Unexpected Error While Reading Payload Length: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
				}
				break
			}
			if i != 8 {
				frameChan <- &frame{
					err: &frameErr{Message: fmt.Sprintf("Insufficient byte read when reading length bytes, possible unexpected EOF: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
				}
				break
			}

			plen = binary.BigEndian.Uint64(lenBytes)
		default:
			plen = uint64(plenb)
		}

		if plen > MAX_PAYLOAD_SIZE {
			frameChan <- &frame{
				err: &frameErr{Message: "Payload size limit exceeded", CloseCode: CLOSE_CODE_PAYLOAD_TOO_BIG},
			}
			break
		}

		maskKey := make([]byte, 0, 4)
		i, err = io.ReadFull(br, maskKey)
		if err != nil {
			frameChan <- &frame{
				err: &frameErr{Message: fmt.Sprintf("Unexpected Error While Reading mask key: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}

		if i != 4 {
			frameChan <- &frame{
				err: &frameErr{Message: fmt.Sprintf("Insufficient byte read when reading mask key, possible unexpected EOF: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}

		payload := make([]byte, 0, plen)
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
				err: &frameErr{Message: fmt.Sprintf("Insufficient byte read when reading payload, possible unexpected EOF: %s", err.Error()), CloseCode: CLOSE_CODE_INTERNEL_SERVER_ERROR},
			}
			break
		}

		for i := uint64(0); i < plen; i++ {
			payload[i] ^= maskKey[i%4]
		}

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
	for true {
		f := <-frameChan

		if f.err != nil {
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
			outChan <- &frame{
				opCode: OP_CODE_CLOSE,
				fin:    true,
			}
			break
		}

		if f.opCode == OP_CODE_PING {
			outChan <- &frame{
				opCode:  OP_CODE_PONG,
				fin:     true,
				payload: f.payload,
			}
			continue
		}

		if f.opCode == OP_CODE_PONG {
			// nothing to do for now, update connection liveliness
			break
		}

		if !f.fin {
			if rootFrame == nil && f.opCode != OP_CODE_BINARY && f.opCode != OP_CODE_TEXT {
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
				rootFrame = f
			}

			fragmentations = append(fragmentations, f.payload...)
			continue
		}

		finalPayload := append(fragmentations, f.payload...)

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
}
