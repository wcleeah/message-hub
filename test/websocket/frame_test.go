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

type goldenTestCase struct {
	Name          string
	SendFrameLocs []string
	ResFrameLocs  []string
	ReadDeadline  time.Duration
	ExpectClose   bool
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
	testCases = append(testCases, invalidTextFrame()...)

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
			if tc.ExpectClose {
				assert.ErrorIs(err, websocket.WebSocketClosed, "Websocket should close")
			} else {
				assert.NoError(err, "Read from websocket should not have error")
				ws.Send(websocket.TextFrame{
					Payload: bs,
				})
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

func validAutoFrame() []*goldenTestCase {
	return []*goldenTestCase{
		{
			Name:          "Ping frame",
			SendFrameLocs: []string{"ping_send_frame.golden"},
			ResFrameLocs:  []string{"ping_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Pong frame",
			SendFrameLocs: []string{"pong_send_frame.golden"},
			ResFrameLocs:  []string{"pong_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Close frame",
			SendFrameLocs: []string{"close_send_frame.golden"},
			ResFrameLocs:  []string{"close_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
	}
}

func invalidAutoFrame() []*goldenTestCase {
	return []*goldenTestCase{
		{
			Name:          "Frame with rsv: 111",
			SendFrameLocs: []string{"rsv111_send_frame.golden"},
			ResFrameLocs:  []string{"rsv_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 110",
			SendFrameLocs: []string{"rsv111_send_frame.golden"},
			ResFrameLocs:  []string{"rsv_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 101",
			SendFrameLocs: []string{"rsv101_send_frame.golden"},
			ResFrameLocs:  []string{"rsv_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 100",
			SendFrameLocs: []string{"rsv100_send_frame.golden"},
			ResFrameLocs:  []string{"rsv_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 011",
			SendFrameLocs: []string{"rsv011_send_frame.golden"},
			ResFrameLocs:  []string{"rsv_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 010",
			SendFrameLocs: []string{"rsv010_send_frame.golden"},
			ResFrameLocs:  []string{"rsv_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frame with rsv: 001",
			SendFrameLocs: []string{"rsv001_send_frame.golden"},
			ResFrameLocs:  []string{"rsv_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Fragmented control frame: close",
			SendFrameLocs: []string{"frag_close_send_frame.golden"},
			ResFrameLocs:  []string{"frag_control_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Fragmented control frame: ping",
			SendFrameLocs: []string{"frag_ping_send_frame.golden"},
			ResFrameLocs:  []string{"frag_control_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Fragmented control frame: pong",
			SendFrameLocs: []string{"frag_pong_send_frame.golden"},
			ResFrameLocs:  []string{"frag_control_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: cond frame",
			SendFrameLocs: []string{"no_mask_cond_send_frame.golden"},
			ResFrameLocs:  []string{"no_mask_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: text frame",
			SendFrameLocs: []string{"no_mask_text_send_frame.golden"},
			ResFrameLocs:  []string{"no_mask_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: binary frame",
			SendFrameLocs: []string{"no_mask_bin_send_frame.golden"},
			ResFrameLocs:  []string{"no_mask_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: ping frame",
			SendFrameLocs: []string{"no_mask_ping_send_frame.golden"},
			ResFrameLocs:  []string{"no_mask_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: pong frame",
			SendFrameLocs: []string{"no_mask_pong_send_frame.golden"},
			ResFrameLocs:  []string{"no_mask_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "No mask: close frame",
			SendFrameLocs: []string{"no_mask_close_send_frame.golden"},
			ResFrameLocs:  []string{"no_mask_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
	}
}

func validTextFrame() []*goldenTestCase {
	return []*goldenTestCase{
		{
			Name:          "No frag",
			SendFrameLocs: []string{"no_frag_send_frame.golden"},
			ResFrameLocs:  []string{"no_frag_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frag Req No Frag Res",
			SendFrameLocs: []string{"frag_send_one_res_1_send_frame.golden", "frag_send_one_res_2_send_frame.golden", "frag_send_one_res_end_send_frame.golden"},
			ResFrameLocs:  []string{"frag_send_one_res_res_frame.golden"},
			ReadDeadline:  5 * time.Second,
		},
		{
			Name:          "Frag Req Frag Res",
			SendFrameLocs: []string{"frag_req_res_1_send_frame.golden", "frag_req_res_2_send_frame.golden", "frag_req_res_end_send_frame.golden"},
			ResFrameLocs:  []string{"frag_req_res_1_res_frame.golden", "frag_req_res_2_res_frame.golden"},
			ReadDeadline:  20 * time.Second,
		},
	}
}

func invalidTextFrame() []*goldenTestCase {
	return []*goldenTestCase{
		{
			Name:          "Frag only frames: one frame holding connection",
			SendFrameLocs: []string{"frag_frame_only_text_send_frame.golden"},
			ResFrameLocs:  []string{},
			ReadDeadline:  3 * time.Second,
			ExpectClose:   true,
		},
		{
			Name:          "Frag only frames: two frame holding connection",
			SendFrameLocs: []string{"frag_frame_only_text_send_frame.golden", "frag_frame_only_cont_send_frame.golden"},
			ResFrameLocs:  []string{},
			ReadDeadline:  3 * time.Second,
			ExpectClose:   true,
		},
		{
			Name: "Frag only frames: too many frag frame",
			SendFrameLocs: []string{
				"frag_frame_only_text_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
				"frag_frame_only_cont_send_frame.golden",
			},
			ResFrameLocs: []string{"too_many_frag_res_frame.golden"},
			ReadDeadline: 5 * time.Second,
			ExpectClose:  true,
		},
		{
			Name:          "Payload too large for a single text frame",
			SendFrameLocs: []string{"humongous_text_send_frame.golden"},
			ResFrameLocs:  []string{"single_frame_too_large_res_frame.golden"},
			ReadDeadline:  1 * time.Minute,
			ExpectClose:   true,
		},
		{
			Name:          "Payload too large for a single cont frame",
			SendFrameLocs: []string{"frag_frame_only_text_send_frame.golden", "humongous_cont_send_frame.golden"},
			ResFrameLocs:  []string{"single_frame_too_large_res_frame.golden"},
			ReadDeadline:  1 * time.Minute,
			ExpectClose:   true,
		},
		{
			Name: "Payload too large for accumulated frames",
			SendFrameLocs: []string{
				"biggest_possible_text_frame_send_frame.golden",
				"biggest_possible_cont_frame_send_frame.golden",
				"biggest_possible_cont_frame_send_frame.golden",
				"biggest_possible_cont_frame_send_frame.golden",
				"biggest_possible_cont_frame_send_frame.golden",
				"biggest_possible_cont_frame_send_frame.golden",
			},
			ResFrameLocs: []string{"accumulated_payload_too_large_res_frame.golden"},
			ReadDeadline: 1 * time.Minute,
			ExpectClose:  true,
		},
		{
			Name:          "First frag frames is cont",
			SendFrameLocs: []string{"frag_frame_only_cont_send_frame.golden"},
			ResFrameLocs:  []string{"first_frag_frame_must_be_text_res_frame.golden"},
			ReadDeadline:  1 * time.Minute,
			ExpectClose:   true,
		},
		{
			Name:          "Frag frames but not cont",
			SendFrameLocs: []string{"frag_frame_only_text_send_frame.golden", "frag_frame_only_text_send_frame.golden"},
			ResFrameLocs:  []string{"cont_frag_frame_must_be_cont_res_frame.golden"},
			ReadDeadline:  1 * time.Minute,
			ExpectClose:   true,
		},
	}
}
