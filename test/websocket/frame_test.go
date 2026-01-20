package websocket_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"com.lwc.message_center_server/internal/logger"
	"com.lwc.message_center_server/internal/websocket"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

type autoFrameTestCase struct {
	Name             string
	SendFrameHeader  []byte
	SendFramePayload []byte
	ExpectedResFrame []byte
	ReadDeadline     time.Duration
	Masked           bool
}

type textFrameTestCase struct {
	Name              string
	SendFramePayloads [][]byte
	ExpectedResFrames [][]byte
	ReadDeadline      time.Duration
}

type goldenTestCase struct {
	Name          string
	SendFrameLocs []string
	ResFrameLocs  []string
	ReadDeadline  time.Duration
}

func readTestData(t *testing.T, loc string) []byte {
	t.Helper()

	fullLoc := filepath.Join("testdata", loc)
	bs, err := os.ReadFile(fullLoc)
	if err != nil {
		t.Fatal(err)
	}

	return bs
}

func TestFrame(t *testing.T) {
	testCases := make([]*goldenTestCase, 0)
	testCases = append(testCases, validAutoFrame()...)
	testCases = append(testCases, invalidAutoFrame()...)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			assert := assert.New(t)
			client, server := net.Pipe()
			client.SetReadDeadline(time.Now().Add(tc.ReadDeadline))

			br := bufio.NewReader(client)
			bw := bufio.NewWriter(client)

			ws := websocket.NewWebSocket(context.Background(), server, tc.ReadDeadline, 0*time.Second)
			ws.Setup()

			for _, sfLoc := range tc.SendFrameLocs {
				sendFrame := readTestData(t, sfLoc)

				i, err := bw.Write(sendFrame)
				assert.Nil(err, "Unexpected error when writing to connection")
				assert.Equal(len(sendFrame), i)
				err = bw.Flush()
				assert.Nil(err, "Unexpected error when flushing buf writer")
			}

			for _, rfLoc := range tc.ResFrameLocs {
				resFrame := readTestData(t, rfLoc)
				resBytes := make([]byte, len(resFrame))

				i, err := io.ReadFull(br, resBytes)
				if errors.Is(err, os.ErrDeadlineExceeded) {
					t.Errorf("Deadline line exceeded, expected %d bytes, read %d bytes, read bytes are: %s", len(resBytes), i, logger.GetPPBytesStr(resBytes[:i]))

					return
				}
				assert.Nilf(err, "Deadline line exceeded, expected %d bytes, read %d bytes, read bytes are: %s", len(resBytes), i, logger.GetPPBytesStr(resBytes[:i]))
				assert.Equal(len(resBytes), i, "Bytes read is different from expected")

				if diff := cmp.Diff(resFrame, resBytes); diff != "" {
					t.Errorf("Response mismatch (-want +got):\n%s", diff)
				}
			}

			client.Close()
			server.Close()
		})
	}
}

func TestTextFrame(t *testing.T) {
	testCases := make([]*goldenTestCase, 0)
	testCases = append(testCases, validTextFrame()...)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			assert := assert.New(t)
			client, server := net.Pipe()
			client.SetReadDeadline(time.Now().Add(tc.ReadDeadline))

			br := bufio.NewReader(client)
			bw := bufio.NewWriter(client)

			ws := websocket.NewWebSocket(context.Background(), server, tc.ReadDeadline, 0*time.Second)
			ws.Setup()

			for _, sfLoc := range tc.SendFrameLocs {
				sendFrame := readTestData(t, sfLoc)

				i, err := bw.Write(sendFrame)
				assert.Nil(err, "Unexpected error when writing to connection")
				assert.Equal(len(sendFrame), i)
				err = bw.Flush()
				assert.Nil(err, "Unexpected error when flushing buf writer")
			}

			bs, err := ws.Read()
			assert.NoError(err, "Read from websocket should not have error")
			ws.Send(websocket.TextFrame{
				Payload: bs,
			})

			for _, rfLoc := range tc.ResFrameLocs {
				resFrame := readTestData(t, rfLoc)
				resBytes := make([]byte, len(resFrame))

				i, err := io.ReadFull(br, resBytes)
				if errors.Is(err, os.ErrDeadlineExceeded) {
					t.Errorf("Deadline line exceeded, expected %d bytes, read %d bytes, read bytes are: %s", len(resBytes), i, logger.GetPPBytesStr(resBytes[:i]))

					return
				}
				assert.Nilf(err, "Deadline line exceeded, expected %d bytes, read %d bytes, read bytes are: %s", len(resBytes), i, logger.GetPPBytesStr(resBytes[:i]))
				assert.Equal(len(resBytes), i, "Bytes read is different from expected")

				if diff := cmp.Diff(resFrame, resBytes); diff != "" {
					t.Errorf("Response mismatch (-want +got):\n%s", diff)
				}
			}

			client.Close()
			server.Close()
		})
	}
}

func validAutoFrame() []*goldenTestCase {
	return []*goldenTestCase{
		{
			Name:          "Ping frame",
			SendFrameLocs: []string{"ping_sendframe.golden"},
			ResFrameLocs:  []string{"ping_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Pong frame",
			SendFrameLocs: []string{"pong_sendframe.golden"},
			ResFrameLocs:  []string{"pong_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Close frame",
			SendFrameLocs: []string{"close_sendframe.golden"},
			ResFrameLocs:  []string{"close_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
	}
}

func invalidAutoFrame() []*goldenTestCase {
	return []*goldenTestCase{
		{
			Name:          "Frame with rsv: 111",
			SendFrameLocs: []string{"rsv111_sendframe.golden"},
			ResFrameLocs:  []string{"rsv_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 110",
			SendFrameLocs: []string{"rsv111_sendframe.golden"},
			ResFrameLocs:  []string{"rsv_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 101",
			SendFrameLocs: []string{"rsv101_sendframe.golden"},
			ResFrameLocs:  []string{"rsv_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 100",
			SendFrameLocs: []string{"rsv100_sendframe.golden"},
			ResFrameLocs:  []string{"rsv_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 011",
			SendFrameLocs: []string{"rsv011_sendframe.golden"},
			ResFrameLocs:  []string{"rsv_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 010",
			SendFrameLocs: []string{"rsv010_sendframe.golden"},
			ResFrameLocs:  []string{"rsv_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 001",
			SendFrameLocs: []string{"rsv001_sendframe.golden"},
			ResFrameLocs:  []string{"rsv_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Fragmented control frame: close",
			SendFrameLocs: []string{"frag_close_sendframe.golden"},
			ResFrameLocs:  []string{"frag_control_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Fragmented control frame: ping",
			SendFrameLocs: []string{"frag_ping_sendframe.golden"},
			ResFrameLocs:  []string{"frag_control_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Fragmented control frame: pong",
			SendFrameLocs: []string{"frame_pong_sendframe.golden"},
			ResFrameLocs:  []string{"frag_control_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: cond frame",
			SendFrameLocs: []string{"nomask_cond_sendframe.golden"},
			ResFrameLocs:  []string{"nomask_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: text frame",
			SendFrameLocs: []string{"nomask_text_sendframe.golden"},
			ResFrameLocs:  []string{"nomask_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: binary frame",
			SendFrameLocs: []string{"nomask_bin_sendframe.golden"},
			ResFrameLocs:  []string{"nomask_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: ping frame",
			SendFrameLocs: []string{"nomask_ping_sendframe.golden"},
			ResFrameLocs:  []string{"nomask_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: pong frame",
			SendFrameLocs: []string{"nomask_pong_sendframe.golden"},
			ResFrameLocs:  []string{"nomask_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: close frame",
			SendFrameLocs: []string{"nomask_close_sendframe.golden"},
			ResFrameLocs:  []string{"nomask_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
	}
}

func validTextFrame() []*goldenTestCase {
	return []*goldenTestCase{
		{
			Name:          "No frag",
			SendFrameLocs: []string{"nofrag_sendframe.golden"},
			ResFrameLocs:  []string{"nofrag_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frag Req No Frag Res",
			SendFrameLocs: []string{"fragsend_1_sendframe.golden", "fragsend_2_sendframe.golden", "fragsend_end_sendframe.golden"},
			ResFrameLocs:  []string{"fragsend_resframe.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frag Req Frag Res",
			SendFrameLocs: []string{"fragreqres_1_sendframe.golden", "fragreqres_2_sendframe.golden", "fragreqres_end_sendframe.golden"},
			ResFrameLocs:  []string{"fragreqres_1_resframe.golden", "fragreqres_2_resframe.golden"},
			ReadDeadline:  20 * time.Second,
		},
	}
}
