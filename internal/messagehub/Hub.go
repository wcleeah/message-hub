package messagehub

import (
	"context"
	"net"
)

type RegMessage struct {
	Ctx context.Context
	Conn net.Conn
}

type MessageHub struct {
	regChan chan RegMessage
}

func NewMessageHug() *MessageHub {
	return &MessageHub{
		regChan: make(chan RegMessage),
	}
}


func (mh *MessageHub) RegChan() (chan RegMessage) {
	return mh.regChan
}
