Full TODO: from zero to a working WebSocket server (Go stdlib only, browser-compatible)
Goal: a browser can do new WebSocket("ws://localhost:8080/ws"), the connection opens, you can receive a text message and send one back.
---

Phase 0 — Basics
-  Decide endpoint: GET /ws
-  Start an HTTP server with net/http on :8080
-  Implement a handler for /ws

---

Phase 1 — HTTP Upgrade handshake (RFC 6455)
1. Validate request
In /ws handler, verify:
- [x] Method is GET
- [x] Connection header contains token Upgrade (case-insensitive, comma-separated)
- [x] Upgrade header equals websocket (case-insensitive)
- [x] Sec-WebSocket-Version equals 13
- [x] Sec-WebSocket-Key is present and looks like base64 (don’t over-validate initially)

2. Compute accept key
- [x] accept = base64( sha1( key + GUID ) )
	- [x] GUID is constant: 258EAFA5-E914-47DA-95CA-C5AB0DC85B11

3. Hijack and reply 101
- [x] Use http.Hijacker to obtain:
	- [x] underlying net.Conn
	- [x] *bufio.ReadWriter (or reader/writer)
- [x] Write response exactly (CRLF matters):
```
	HTTP/1.1 101 Switching Protocols\r\n
	Upgrade: websocket\r\n
	Connection: Upgrade\r\n
	Sec-WebSocket-Accept: <computed>\r\n
	\r\n
```
- [x] Flush the writer
- [x] From this point on, do not use http.ResponseWriter for this connection

At the end of Phase 1:
- Browser onopen should fire (or at least not fail immediately). But it may still close if you don’t handle frames correctly.

---

Phase 2 — WebSocket frame parsing (minimum viable)
You must implement server-side receiving of frames from the browser.

4. Implement ReadFrame(conn) (from TCP stream)
Read in this order from the connection:

4.1 First 2 bytes
-  Read 2 bytes: b0, b1
- Parse:
	-  fin := (b0 & 0x80) != 0
	-  opcode := b0 & 0x0F
	-  masked := (b1 & 0x80) != 0
	-  plen7 := b1 & 0x7F

4.2 Validate masking
-  From browsers, client-to-server frames MUST be masked
	- If masked == false, treat as protocol error and close.

4.3 Determine payload length
-  If plen7 <= 125: payload length = plen7
-  If plen7 == 126: read next 2 bytes (big endian) for length
-  If plen7 == 127: read next 8 bytes (big endian) for length
	- For learning, you can reject absurdly large sizes (set a max, e.g. 1–16MB)

4.4 Read masking key
-  Read 4 bytes mask key

4.5 Read payload
-  Read payloadLen bytes

4.6 Unmask
-  For each byte i: payload[i] ^= maskKey[i%4]

Now you have one frame: (fin, opcode, payload).

5. Handle opcodes (minimum set)
Implement these behaviors:
- 0x1 Text frame:
	-  payload must be valid UTF-8 (browsers expect correctness; you can optionally validate)
	-  deliver it to your app (for now maybe log it and echo)
- 0x8 Close:
	-  Reply with a close frame (echo status code if provided)
	-  Then close TCP connection
- 0x9 Ping:
	-  Reply with Pong (0xA) with same payload
- 0xA Pong:
	-  Accept/ignore (useful later for heartbeats)

6. Fragmentation (FIN=false)

For a “just get it working” server, you have two options:

-  If you get opcode=0x1 with fin=false, start a buffer
-  Next frames should be opcode=0x0 (continuation)
-  Append payloads until fin=true
-  Treat assembled bytes as one text message

For browser interoperability, Option B is more robust, but Option A is okay for early learning if you keep messages small.

---

Phase 3 — Server-to-browser frame writing
Browsers expect the server to send unmasked frames.

7. Implement WriteFrame(conn, opcode, payload)
-  Set FIN=1, RSV=0
-  First byte: 0x80 | opcode
-  Second byte: mask bit = 0, payload length encoding:
	-  <=125: 1 byte
	-  <=65535: marker 126 + 2 bytes length (big endian)
	-  else: marker 127 + 8 bytes length (big endian)
-  Write header then payload bytes

8. Send at least one server message
To prove it works:
-  On successful handshake, send a text frame: "welcome"
-  Or echo any received text messages back

---

Phase 4 — Connection lifecycle, concurrency, safety

9. Concurrency rule (per connection)
Implement:
-  1 goroutine reading frames from the conn
-  1 goroutine writing frames to the conn
-  A send chan []byte for outbound messages
-  No other goroutine writes to the conn

Even if you don’t have pub/sub yet, adopt this now—it prevents later rewrites.

10. Deadlines / timeouts (highly recommended)
-  Set read deadline and extend it when you receive pong (or any frame)
-  Periodically send ping frames from writer goroutine
-  If ping/pong fails, close

For a first milestone, you can skip deadlines, but be aware dead sockets may linger.

11. Clean close procedure
-  When you decide to close:
	- send close frame
	- close TCP conn
-  Ensure goroutines exit (closing send channel, etc.)

---

Phase 5 — Testing checklist (browser)

Create a simple HTML page:
-  Connect with new WebSocket("ws://localhost:8080/ws")
-  Log onopen, onmessage, onclose, onerror
-  Send a message on open: ws.send("hello")
-  Confirm server logs it and echoes back
-  Test closing:
	- from browser: ws.close()
	- from server: send close after N seconds
