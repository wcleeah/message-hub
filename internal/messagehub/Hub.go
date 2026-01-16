package messagehub

import (
	"context"
	"net"
	"time"

	"com.lwc.message_center_server/internal/assert"
	"com.lwc.message_center_server/internal/logger"
	"com.lwc.message_center_server/internal/websocket"
	"github.com/google/uuid"
)

type Client struct {
	Ctx        context.Context
	Conn       net.Conn
	Id         uuid.UUID
}

type MessageHub struct {
	regChan   chan *Client
	unregChan chan uuid.UUID
	clients   map[uuid.UUID]*Client
}

func NewMessageHub() *MessageHub {
	return &MessageHub{
		regChan:   make(chan *Client, 10),
		unregChan: make(chan uuid.UUID, 10),
		clients: make(map[uuid.UUID]*Client),
	}
}

func (mh *MessageHub) Start() error {
	go mh.registerClient()
	go mh.unregisterClient()
	return nil
}

func (mh *MessageHub) registerClient() {
	for true {
		client := <-mh.regChan
		_, ok := mh.clients[client.Id]

		// should use net.conn address? or socket combination?
		assert.Assert(!ok, "Client has been registered before, or id is used before")

		mh.clients[client.Id] = client
		go mh.handleEvent(client)
	}
}

func (mh *MessageHub) unregisterClient() {
	for true {
		id := <-mh.unregChan
		_, ok := mh.clients[id]
		assert.Assert(ok, "Client is never registered")

		mh.clients[id] = nil
	}
}

func (mh *MessageHub) handleEvent(c *Client) {
	l := logger.Get(c.Ctx)
	ws := websocket.NewWebSocket(c.Ctx, c.Conn, 30*time.Second, 5*time.Second)
	ws.Setup()

	for true {
		bs, err := ws.Read()
		if err != nil {
			l.Error("Event handler: Error during reading from ws, breaking", "err", err)
			break
		}

		// for now echoing back the payload

		err = ws.Send(&websocket.TextFrame{
			Payload: bs,
		})
		if err != nil {
			l.Error("Event handler: Error during sending to ws, breaking", "err", err)
			break
		}
	}

	c.Conn.Close()

	// unreg
	mh.unregChan <- c.Id
}

func (mh *MessageHub) RegChan() chan *Client {
	return mh.regChan
}
