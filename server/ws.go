package server

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Yonsm/MiHex/core"
)

const (
	maxConns   = 8
	maxPayload = 65536
	wsGUID     = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

// Hub 维护 WebSocket 连接，并实现 core.Broadcaster。
type Hub struct {
	mu    sync.Mutex
	conns map[*conn]struct{}
	slots chan struct{}
}

// conn 是一条已升级的 WebSocket 连接（RFC 6455，服务端侧）。
type conn struct {
	nc   net.Conn
	br   *bufio.Reader
	send chan []byte
	wmu  sync.Mutex
	once sync.Once
}

func (h *Hub) add(c *conn) {
	h.mu.Lock()
	n := len(h.conns)
	h.conns[c] = struct{}{}
	if n == 0 {
		core.Fast()
	}
	h.mu.Unlock()
}

func (h *Hub) drop(c *conn) {
	h.mu.Lock()
	if _, ok := h.conns[c]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.conns, c)
	if len(h.conns) == 0 {
		core.Slow()
	}
	h.mu.Unlock()
	c.shutdown()
}

// closeAll 关闭所有连接（进程退出时调用）。
func (h *Hub) closeAll() {
	h.mu.Lock()
	conns := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		h.drop(c)
	}
}

// Send 广播一条消息；发送缓冲满的慢客户端直接断开。
func (h *Hub) Send(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	var slow []*conn
	h.mu.Lock()
	for c := range h.conns {
		select {
		case c.send <- data:
		default:
			slow = append(slow, c)
		}
	}
	h.mu.Unlock()
	for _, c := range slow {
		h.drop(c)
	}
}

func (h *Hub) handle(w http.ResponseWriter, r *http.Request) {
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Unsupported", http.StatusInternalServerError)
		return
	}
	nc, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	if _, err := brw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"); err != nil {
		nc.Close()
		return
	}
	if err := brw.Flush(); err != nil {
		nc.Close()
		return
	}
	c := &conn{nc: nc, br: brw.Reader, send: make(chan []byte, 64)}
	h.add(c)
	go c.writeLoop()
	defer h.drop(c)
	c.readLoop()
}

func (c *conn) shutdown() {
	c.once.Do(func() {
		close(c.send)
		c.nc.Close()
	})
}

func (c *conn) writeLoop() {
	for data := range c.send {
		if err := c.writeFrame(0x1, data); err != nil {
			c.nc.Close()
			return
		}
	}
}

func (c *conn) readLoop() {
	var buf []byte
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			slog.Debug("WebSocket 读结束", "err", err)
			return
		}
		switch opcode {
		case 0x8: // close
			c.writeFrame(0x8, nil)
			return
		case 0x9: // ping
			c.writeFrame(0xA, payload)
			continue
		case 0xA: // pong
			continue
		case 0x0: // continuation
			buf = append(buf, payload...)
			if len(buf) > maxPayload {
				return
			}
			if fin {
				onMessage(buf)
				buf = nil
			}
		case 0x1, 0x2:
			buf = payload
			if fin {
				onMessage(buf)
				buf = nil
			}
		}
	}
}

func (c *conn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var head [2]byte
	if _, err = io.ReadFull(c.br, head[:]); err != nil {
		return
	}
	fin = head[0]&0x80 != 0
	opcode = head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := int(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint64(ext[:])
		if n > maxPayload {
			err = errors.New("payload too large")
			return
		}
		length = int(n)
	}
	if length > maxPayload {
		err = errors.New("payload too large")
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return
}

func (c *conn) writeFrame(opcode byte, payload []byte) error {
	n := len(payload)
	head := make([]byte, 0, 10)
	head = append(head, 0x80|opcode)
	switch {
	case n < 126:
		head = append(head, byte(n))
	case n < 65536:
		head = append(head, 126, byte(n>>8), byte(n))
	default:
		head = append(head, 127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := c.nc.Write(append(head, payload...)); err != nil {
		return err
	}
	return nil
}

// onMessage 与 Python 版一致：数组是命令，对象是服务调用，结果广播给所有连接。
func onMessage(data []byte) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		hub.Send("WebSocket error: " + err.Error())
		return
	}
	switch x := v.(type) {
	case []any:
		if len(x) == 0 {
			return
		}
		name, _ := x[0].(string)
		go func() {
			r, err := execCommand(x)
			if err != nil {
				slog.Debug("WS 命令失败", "name", name, "err", err)
				hub.Send("Execution error: " + err.Error())
				return
			}
			hub.Send(map[string]any{name: r})
		}()
	case map[string]any:
		call, _ := x["call"].(string)
		fn := services[call]
		if fn == nil {
			hub.Send("Unknown service call: " + call)
			return
		}
		r, err := fn(x)
		if err != nil {
			hub.Send("WebSocket error: " + err.Error())
			return
		}
		hub.Send(r)
	}
}
