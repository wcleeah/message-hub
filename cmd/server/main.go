package main

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"com.lwc.message_center_server/internal/logger"
	"com.lwc.message_center_server/internal/messagehub"
)

const GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func main() {
	logger.Setup(slog.LevelDebug)
	mux := http.NewServeMux()
	mh := messagehub.NewMessageHug()
	go mh.Start()

	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		connCtx, l := logger.GetWithValue(r.Context())
		l.Debug("Got something...")

		connectionHeader := r.Header.Get("Connection")
		if !strings.Contains(strings.ToLower(connectionHeader), "upgrade") {
			l.Debug("Connection Header Invalid", "header", connectionHeader)
			w.WriteHeader(http.StatusBadRequest)
			resText := "Connection header must contain \"Upgrade\" to indicate your intent"

			w.Write([]byte(resText))
			return
		}

		upgradeHeader := r.Header.Get("Upgrade")
		if strings.ToLower(upgradeHeader) != "websocket" {
			l.Debug("Upgrade Header Invalid", "header", upgradeHeader)
			w.WriteHeader(http.StatusBadRequest)
			resText := "Only websocket protocol upgrade is supported"

			w.Write([]byte(resText))
			return
		}

		wsVersionHeader := r.Header.Get("Sec-WebSocket-Version")
		if wsVersionHeader != "13" {
			l.Debug("Socket Version Invalid", "version", wsVersionHeader)
			w.WriteHeader(http.StatusUpgradeRequired)
			resText := "Only v13 is supported please upgrade"

			w.Write([]byte(resText))
			return
		}

		wsKeyHeader := r.Header.Get("Sec-WebSocket-Key")
		if wsKeyHeader != "" {
			decodedKey, err := base64.StdEncoding.DecodeString(wsKeyHeader)
			if err != nil {
				l.Debug("Socket Key Decode Error", "error", err.Error())
				w.WriteHeader(http.StatusBadRequest)
				resText := "Failed to decode Sec-WebSocket-Key"

				w.Write([]byte(resText))
				return
			}

			if len(decodedKey) != 16 {
				l.Debug("Socket Key Length Invalid", "length", len(decodedKey))
				w.WriteHeader(http.StatusBadRequest)
				resText := "Sec-WebSocket-Key must be 16 bytes long"

				w.Write([]byte(resText))
				return
			}
		} else {
			l.Debug("Sec-WebSocket-Key is required for the upgrade")
			w.WriteHeader(http.StatusBadRequest)
			resText := "Sec-WebSocket-Key is required for the upgrade"

			w.Write([]byte(resText))
			return
		}

		l.Debug("Validation passes, proceeding to hijacking...")
		hijacker := w.(http.Hijacker)
		conn, bw, err := hijacker.Hijack()
		if err != nil {
			l.Debug("Hijack fails", "error", err.Error())
			w.WriteHeader(http.StatusInternalServerError)

			resText := "Internal server error: hijack failed"
			w.Write([]byte(resText))

			return
		}

		l.Debug("Hijack completed, responding...")
		shaRes := sha1.Sum([]byte(wsKeyHeader + GUID))
		acceptKey := base64.StdEncoding.EncodeToString(shaRes[:])

		var sb strings.Builder
		sb.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		sb.WriteString("Upgrade: websocket\r\n")
		sb.WriteString("Connection: Upgrade\r\n")
		sb.WriteString("Sec-WebSocket-Accept: ")
		sb.WriteString(acceptKey)
		sb.WriteString("\r\n")
		sb.WriteString("\r\n")

		str := sb.String()
		l.Debug(str)
		bw.WriteString(sb.String())
		err = bw.Flush()
		if err != nil {
			l.Debug("Flush failed", "error", err.Error())
			conn.Close()
			return
		}
		l.Debug("Passing control to message hub")

		regMsg := &messagehub.RegMessage{
			Ctx:  connCtx,
			Conn: conn,
		}

		mh.RegChan() <- regMsg
	})

	fmt.Print("server starting...\n")
	err := http.ListenAndServe(":42069", mux)

	panic(err)
}
