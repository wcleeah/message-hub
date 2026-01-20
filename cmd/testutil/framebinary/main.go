package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var rsvMap map[int]map[string]byte = map[int]map[string]byte{
	0: {
		"0": 0x0,
		"1": 0x40,
	},
	1: {
		"0": 0x0,
		"1": 0x20,
	},
	2: {
		"0": 0x0,
		"1": 0x10,
	},
}

const padToken = 0x41

var utf8Tokens = [][]byte{
	[]byte("l"), []byte("é"), []byte("ï"),
	[]byte("ç"), []byte("é"), []byte("ñ"),
	[]byte("こ"), []byte("界"), []byte("あ"),
	[]byte("П"), []byte("р"), []byte("Γ"),
	[]byte("σ"), []byte("ε"), []byte("م"),
	[]byte("ا"), []byte(""), []byte("दु"),
	[]byte("試"), []byte("🙂"),
}

func findGoModRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("Weird...")
		}
		dir = parent
	}
}

func genBinary(bl int) []byte {
	if bl <= 0 {
		panic("Bytes length must be bigger than 0")
	}

	var buf bytes.Buffer
	buf.Grow(bl)
	tokenIdx := 0
	for buf.Len() < bl {
		token := utf8Tokens[tokenIdx%len(utf8Tokens)]
		if buf.Len()+len(token) > bl {
			buf.WriteByte(padToken)
			continue
		}

		buf.Write(token)
		tokenIdx++
	}

	return buf.Bytes()
}

func ppHexBytes(bs []byte) string {
	var sb strings.Builder
	for i, b := range bs {
		fmt.Fprintf(&sb, "0x%02X", b)
		if i != len(bs)-1 {
			sb.WriteString(", ")
		}
	}

	return sb.String()
}

func maskKey() [4]byte {
	var key [4]byte
	rand.Read(key[:])
	return key
}

func ask(q string, defaultAns string, reader *bufio.Reader) string {
	fmt.Printf("%s: ", q)
	ans, err := reader.ReadString('\n')
	if err != nil {
		panic(err)
	}

	ans = strings.TrimSpace(ans)
	if ans == "" {
		return defaultAns
	} else {
		return ans
	}
}

func buildSendRes(baseDir string, reader *bufio.Reader) error {
	var lenHdr byte
	payloadBytes := []byte{}
	lenBytes := []byte{}
	mk := maskKey()

	ccAns := ask("Close Code", "", reader)
	if ccAns != "" {
		i, err := strconv.ParseUint(ccAns, 10, 16)
		if err != nil {
			return errors.New("Close Code: Invalid close code")
		}

		code := uint16(i)
		highByte := byte(code >> 8)
		lowByte := byte(code)
		payloadBytes = append(payloadBytes, highByte, lowByte)
	}

	payloadModeAns := ask("Payload (raw: r, len: l, empty: e, default e)", "e", reader)
	switch payloadModeAns {
	case "l":
		lenAns := ask("Payload length (int / ^<power of two>", "", reader)
		if lenAns == "" {
			return errors.New("Payload length: Cannot be empty")
		}
		if after, ok := strings.CutPrefix(lenAns, "^"); ok {
			powerToTwo, err := strconv.Atoi(after)
			if err != nil {
				return errors.New("Payload length: Not an int")
			}
			payloadBytes = append(payloadBytes, genBinary(1<<powerToTwo)...)
		} else {
			payloadLen, err := strconv.Atoi(lenAns)
			if err != nil {
				return errors.New("Payload length: Not an int")
			}
			payloadBytes = append(payloadBytes, genBinary(payloadLen)...)
		}

	case "r":
		payloadAns := ask("Payload string", "", reader)
		if payloadAns == "" {
			return errors.New("Payload string: Cannot be empty")
		}

		payloadBytes = append(payloadBytes, []byte(payloadAns)...)
	case "e":
		break
	default:
		return errors.New("Payload: Invalid options")
	}

	payloadLen := len(payloadBytes)
	switch {
	case payloadLen <= 125:
		lenHdr |= byte(payloadLen)
	case payloadLen <= 65535:
		lenHdr |= byte(126)
		lenBytes = make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(payloadLen))
	default:
		lenHdr |= byte(127)
		lenBytes = make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(payloadLen))
	}

	for range 2 {
		var hdr1 byte
		var hdr2 byte
		var masked bool
		fullHdr := []byte{}
		framePayloadBytes := make([]byte, len(payloadBytes))

		fmt.Print("\nBuilding new frame...\n")
		finAns := ask("Fin? (y/n, default n)", "n", reader)
		switch finAns {
		case "y":
			hdr1 = 0x80
		case "n":
			hdr1 = 0x00
		default:
			return errors.New("Fin: Invalid options")
		}

		rsvAns := ask("RSV? (n / 000, 001, 010..., default n)", "n", reader)
		if rsvAns != "n" {
			for i, r := range rsvAns {
				if i > 2 {
					return errors.New("RSV: Invalid options")
				}
				str := string(r)
				bm, _ := rsvMap[i]
				b, ok := bm[str]
				if !ok {
					return errors.New("RSV: Invalid options")
				}
				hdr1 |= b
			}
		}

		opCodeAns := ask("Op Code? (cond / text / binary / close / ping / pong)", "", reader)
		switch strings.TrimSpace(opCodeAns) {
		case "cond":
			hdr1 |= 0x00
		case "text":
			hdr1 |= 0x01
		case "binary":
			hdr1 |= 0x02
		case "close":
			hdr1 |= 0x08
		case "ping":
			hdr1 |= 0x09
		case "pong":
			hdr1 |= 0x0A
		default:
			return errors.New("Op Code: Invalid options")
		}

		maskAns := ask("Masked? (y / n, default n)", "n", reader)

		switch maskAns {
		case "y":
			hdr2 = 0x80 | lenHdr
			masked = true
		case "n":
			hdr2 = 0x0 | lenHdr
			masked = false
		default:
			return errors.New("Masked: Invalid options")
		}

		if masked {
			for i := range len(payloadBytes) {
				framePayloadBytes[i] = payloadBytes[i] ^ mk[i%4]
			}
		} else {
			for i := range len(payloadBytes) {
				framePayloadBytes[i] = payloadBytes[i]
			}
		}

		fullHdr = append(fullHdr, hdr1, hdr2)
		fullHdr = append(fullHdr, lenBytes...)

		wpAns := ask("Print or Write (p / w, default p)", "p", reader)
		if wpAns != "p" && wpAns != "w" {
			return errors.New("Print or Write: Invalid options")
		}
		if wpAns == "p" {
			fmt.Printf("hdr: []byte{%s}\n", ppHexBytes(fullHdr))
			if payloadLen > 0 {
				fmt.Printf("payload: []byte{%s}\n", ppHexBytes(framePayloadBytes))
				fmt.Printf("all in one: []byte{%s, %s}\n", ppHexBytes(fullHdr), ppHexBytes(framePayloadBytes))
			}
			return nil
		}

		locAns := ask("Location", "", reader)
		if locAns == "" {
			return errors.New("Location: Invalid options")
		}

		rootDir := findGoModRoot()
		destStr := filepath.Join(rootDir, baseDir, locAns)

		writePartAns := ask("What to write (full frame: ff, payload only: p, header only (w/ mask key if any): h, default ff)", "ff", reader)
		bs := make([]byte, 0)
		switch strings.TrimSpace(writePartAns) {
		case "ff":
			bs = append(bs, fullHdr...)
			if masked {
				bs = append(bs, mk[:]...)
			}
			if len(payloadBytes) > 0 {
				bs = append(bs, framePayloadBytes...)
			}
		case "p":
			if len(payloadBytes) > 0 {
				bs = append(bs, framePayloadBytes...)
			}
		case "h":
			bs = append(bs, fullHdr...)
			if masked {
				bs = append(bs, mk[:]...)
			}
		default:
			return errors.New("What to write: Invalid options")
		}

		if err := os.WriteFile(destStr, bs, 0o644); err != nil {
			return fmt.Errorf("Failed to write to %s", destStr)
		}

		fmt.Printf("Bytes written to %s\n", destStr)
	}
	return nil
}

func build(baseDir string, reader *bufio.Reader) error {
	var hdr1 byte
	var hdr2 byte
	var masked bool
	payloadBytes := []byte{}
	lenBytes := []byte{}
	fullHdr := []byte{}
	mk := maskKey()

	finAns := ask("Fin? (y/n, default n)", "n", reader)
	switch finAns {
	case "y":
		hdr1 = 0x80
	case "n":
		hdr1 = 0x00
	default:
		return errors.New("Fin: Invalid options")
	}

	rsvAns := ask("RSV? (n / 000, 001, 010..., default n)", "n", reader)
	if rsvAns != "n" {
		for i, r := range rsvAns {
			if i > 2 {
				return errors.New("RSV: Invalid options")
			}
			str := string(r)
			bm, _ := rsvMap[i]
			b, ok := bm[str]
			if !ok {
				return errors.New("RSV: Invalid options")
			}
			hdr1 |= b
		}
	}

	opCodeAns := ask("Op Code? (cond / text / binary / close / ping / pong)", "", reader)
	switch strings.TrimSpace(opCodeAns) {
	case "cond":
		hdr1 |= 0x00
	case "text":
		hdr1 |= 0x01
	case "binary":
		hdr1 |= 0x02
	case "close":
		hdr1 |= 0x08
	case "ping":
		hdr1 |= 0x09
	case "pong":
		hdr1 |= 0x0A
	default:
		return errors.New("Op Code: Invalid options")
	}

	maskAns := ask("Masked? (y / n, default n)", "n", reader)

	switch maskAns {
	case "y":
		hdr2 = 0x80
		masked = true
	case "n":
		hdr2 = 0x0
		masked = false
	default:
		return errors.New("Masked: Invalid options")
	}

	ccAns := ask("Close Code", "", reader)
	if ccAns != "" {
		i, err := strconv.ParseUint(ccAns, 10, 16)
		if err != nil {
			return errors.New("Close Code: Invalid close code")
		}

		code := uint16(i)
		highByte := byte(code >> 8)
		lowByte := byte(code)
		payloadBytes = append(payloadBytes, highByte, lowByte)
	}

	payloadModeAns := ask("Payload (raw: r, len: l, empty: e, default e)", "e", reader)
	switch payloadModeAns {
	case "l":
		lenAns := ask("Payload length (int / ^<power of two>", "", reader)
		if lenAns == "" {
			return errors.New("Payload length: Cannot be empty")
		}
		if after, ok := strings.CutPrefix(lenAns, "^"); ok {
			powerToTwo, err := strconv.Atoi(after)
			if err != nil {
				return errors.New("Payload length: Not an int")
			}
			payloadBytes = append(payloadBytes, genBinary(1<<powerToTwo)...)
		} else {
			payloadLen, err := strconv.Atoi(lenAns)
			if err != nil {
				return errors.New("Payload length: Not an int")
			}
			payloadBytes = append(payloadBytes, genBinary(payloadLen)...)
		}

	case "r":
		payloadAns := ask("Payload string", "", reader)
		if payloadAns == "" {
			return errors.New("Payload string: Cannot be empty")
		}

		payloadBytes = append(payloadBytes, []byte(payloadAns)...)
	case "e":
		break
	default:
		return errors.New("Payload: Invalid options")
	}

	if masked {
		for i := range len(payloadBytes) {
			payloadBytes[i] = payloadBytes[i] ^ mk[i%4]
		}
	}

	payloadLen := len(payloadBytes)
	switch {
	case payloadLen <= 125:
		hdr2 |= byte(payloadLen)
	case payloadLen <= 65535:
		hdr2 |= byte(126)
		lenBytes = make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(payloadLen))
	default:
		hdr2 |= byte(127)
		lenBytes = make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(payloadLen))
	}

	fullHdr = append(fullHdr, hdr1, hdr2)
	fullHdr = append(fullHdr, lenBytes...)

	wpAns := ask("Print or Write (p / w, default p)", "p", reader)
	if wpAns != "p" && wpAns != "w" {
		return errors.New("Print or Write: Invalid options")
	}
	if wpAns == "p" {
		fmt.Printf("hdr: []byte{%s}\n", ppHexBytes(fullHdr))
		if payloadLen > 0 {
			fmt.Printf("payload: []byte{%s}\n", ppHexBytes(payloadBytes))
			fmt.Printf("all in one: []byte{%s, %s}\n", ppHexBytes(fullHdr), ppHexBytes(payloadBytes))
		}
		return nil
	}

	locAns := ask("Location", "", reader)
	if locAns == "" {
		return errors.New("Location: Invalid options")
	}

	rootDir := findGoModRoot()
	destStr := filepath.Join(rootDir, baseDir, locAns)

	writePartAns := ask("What to write (full frame: ff, payload only: p, header only (w/ mask key if any): h, default ff)", "ff", reader)
	bs := make([]byte, 0)
	switch strings.TrimSpace(writePartAns) {
	case "ff":
		bs = append(bs, fullHdr...)
		if masked {
			bs = append(bs, mk[:]...)
		}
		if len(payloadBytes) > 0 {
			bs = append(bs, payloadBytes...)
		}
	case "p":
		if len(payloadBytes) > 0 {
			bs = append(bs, payloadBytes...)
		}
	case "h":
		bs = append(bs, fullHdr...)
		if masked {
			bs = append(bs, mk[:]...)
		}
	default:
		return errors.New("What to write: Invalid options")
	}

	if err := os.WriteFile(destStr, bs, 0o644); err != nil {
		return fmt.Errorf("Failed to write to %s", destStr)
	}

	fmt.Printf("Bytes written to %s\n", destStr)
	return nil
}

func main() {
	var baseDir string
	reader := bufio.NewReader(os.Stdin)
Outer:
	for {
		action := ask("\nWhat do you wanna do? (build: b, build send and res frame: bsr, set base dir: sbd, clean: c, exit: e)", "b", reader)
		switch action {
		case "b":
			err := build(baseDir, reader)
			if err != nil {
				fmt.Printf("Error during build: %s\n", err.Error())
			}
			reader.Discard(reader.Buffered())
		case "bsr":
			err := buildSendRes(baseDir, reader)
			if err != nil {
				fmt.Printf("Error during build: %s\n", err.Error())
			}
			reader.Discard(reader.Buffered())
		case "sbd":
			baseDir = ask("Base dir?", "", reader)
			fmt.Printf("Base dir set to %s\n", baseDir)
		case "e":
			fmt.Print("Goodbye.\n")
			break Outer
		case "c":
			fmt.Print("\x1b[2J\x1b[H")
		}
	}
}
