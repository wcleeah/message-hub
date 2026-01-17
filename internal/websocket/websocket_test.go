package websocket_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"com.lwc.message_center_server/internal/logger"
	"com.lwc.message_center_server/internal/websocket"
	"github.com/stretchr/testify/assert"
)

type testCase struct {
	Name             string
	SendFrameHeader  []byte
	SendFramePayload []byte
	ExpectedResFrame []byte
	ReadDeadline     time.Duration
}

func TestAutoFrame(t *testing.T) {
	mk := maskKey(t)

	testCases := make([]*testCase, 0)
	testCases = append(testCases, validAutoFrame()...)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			client, server := net.Pipe()

			client.SetReadDeadline(time.Now().Add(tc.ReadDeadline))

			br := bufio.NewReader(client)
			bw := bufio.NewWriter(client)

			var wg sync.WaitGroup
			go read(t, br, tc.ExpectedResFrame, &wg)
			wg.Add(1)
			frame := make([]byte, 0)
			frame = append(frame, tc.SendFrameHeader...)
			frame = append(frame, mk[:]...)
			frame = append(frame, tc.SendFramePayload...)

			ws := websocket.NewWebSocket(context.Background(), server, tc.ReadDeadline, 0*time.Second)
			ws.Setup()

			i, err := bw.Write(frame)
			assert.Nil(t, err, "Unexpected error when writing to connection")
			assert.Equal(t, len(frame), i)
			err = bw.Flush()
			assert.Nil(t, err, "Unexpected error when flushing buf writer")

			wg.Wait()
			client.Close()
			server.Close()
		})
	}
}

func maskKey(t *testing.T) [4]byte {
	var key [4]byte
	_, err := rand.Read(key[:])
	assert.Nil(t, err, "Unexpected error when generating mask key")
	return key
}

func read(t *testing.T, reader io.Reader, expectedBytes []byte, wg *sync.WaitGroup) {
	defer wg.Done()
	resBytes := make([]byte, len(expectedBytes))

	i, err := io.ReadFull(reader, resBytes)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Deadline line exceeded, expected %d bytes, read %d bytes, read bytes are: %s", len(expectedBytes), i, logger.GetPPBytesStr(resBytes[:i]))

		return
	}
	assert.Nilf(t, err, "Deadline line exceeded, expected %d bytes, read %d bytes, read bytes are: %s", len(expectedBytes), i, logger.GetPPBytesStr(resBytes[:i]))
	assert.Equal(t, len(expectedBytes), i, "Bytes read is different from expected")

	for i, rb := range resBytes {
		eb := expectedBytes[i]
		assert.Equal(t, eb, rb)
	}
}

func validAutoFrame() []*testCase {
	return []*testCase{
		{
			Name:             "Ping frame",
			SendFrameHeader:  []byte{0x89, 0x80},
			SendFramePayload: []byte{},
			ExpectedResFrame: []byte{0x8A, 0x0},
			ReadDeadline:     5 * time.Second,
		},
		{
			Name:             "Pong frame",
			SendFrameHeader:  []byte{0x8A, 0x80},
			SendFramePayload: []byte{},
			ExpectedResFrame: []byte{},
			ReadDeadline:     5 * time.Second,
		},
		{
			Name:             "Close frame",
			SendFrameHeader:  []byte{0x88, 0x80},
			SendFramePayload: []byte{},
			ExpectedResFrame: []byte{0x88, 0x0},
			ReadDeadline:     5 * time.Second,
		},
	}
}
