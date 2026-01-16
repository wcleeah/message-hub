package websocket

import (
	"errors"
	"fmt"
)

const (
	CLOSE_CODE_PROTOCOL_VIOLATION    uint16 = 1009
	CLOSE_CODE_INVALID_PAYLOAD       uint16 = 1007
	CLOSE_CODE_PAYLOAD_TOO_BIG       uint16 = 1009
	CLOSE_CODE_INTERNEL_SERVER_ERROR uint16 = 1011
)

var (
	WebSocketClosed = errors.New("WebSocket is closed")
)

type frameErr struct {
	Message   string
	CloseCode uint16
}

func (pv frameErr) Error() string {
	return fmt.Sprintf("%s: %s", pv.CloseCode, pv.Message)
}

func (pv frameErr) ToPayload() []byte {
	code := pv.CloseCode
	highByte := byte(code >> 8)
	lowByte := byte(code)
	payload := []byte{highByte, lowByte}

	return append(payload, []byte(pv.Message)...)
}
