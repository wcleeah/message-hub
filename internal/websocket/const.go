package websocket

const (
	OP_CODE_CONTINUATION byte = 0x00
	OP_CODE_TEXT         byte = 0x01
	OP_CODE_BINARY       byte = 0x02
	OP_CODE_CLOSE        byte = 0x08
	OP_CODE_PING         byte = 0x09
	OP_CODE_PONG         byte = 0x0A
	MAX_PAYLOAD_SIZE          = 1 << 20
)
