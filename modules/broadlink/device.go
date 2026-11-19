package broadlink

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"time"
)

var (
	magic   = []byte{0x5a, 0xa5, 0xaa, 0x55, 0x5a, 0xa5, 0xaa, 0x55}
	initKey = []byte{0x09, 0x76, 0x28, 0x34, 0x3f, 0xe9, 0x9e, 0x23, 0x76, 0x5c, 0x15, 0x13, 0xac, 0xcf, 0x8b, 0x02}
	iv      = []byte{0x56, 0x2e, 0x17, 0x99, 0x6d, 0x09, 0x3d, 0x28, 0xdd, 0xb3, 0xba, 0x69, 0x5a, 0x2e, 0x6f, 0x58}
	le      = binary.LittleEndian
)

type family uint8

const (
	unknown family = iota
	sp1
	sp2
	sp3
	sp4
	sp4b
	rm
	rm4
)

func model(id uint16) family {
	switch id {
	case 0x0000:
		return sp1
	case 0x2711, 0x2716, 0x2717, 0x2719, 0x271a, 0x271d, 0x2720, 0x2728, 0x2736, 0x273e, 0x7530, 0x7539, 0x753e, 0x7540, 0x7544, 0x7546, 0x7547, 0x7549, 0x7918, 0x7919, 0x791a, 0x7d0d, 0x9479, 0x947a:
		return sp2
	case 0x2733, 0x7d00:
		return sp3
	case 0x7568, 0x756b, 0x756c, 0x756f, 0x7579, 0x757b, 0x7583, 0x7587, 0x7d11, 0xa4f9, 0xa569, 0xa56a, 0xa56b, 0xa56c, 0xa576, 0xa589, 0xa5d3, 0xa6f4:
		return sp4
	case 0x5115, 0x51e2, 0x51e3, 0x6111, 0x6113, 0x618b, 0x6480, 0x6489, 0x648b, 0x648c, 0x6494:
		return sp4b
	case 0x2712, 0x272a, 0x2737, 0x273d, 0x277c, 0x2783, 0x2787, 0x278b, 0x278f, 0x2797, 0x279d, 0x27a1, 0x27a6, 0x27a9, 0x27b7, 0x27c2, 0x27c3, 0x27c7, 0x27cc, 0x27cd, 0x27d0, 0x27d1, 0x27d3, 0x27dc, 0x27de:
		return rm
	case 0x51da, 0x5209, 0x520b, 0x520c, 0x520d, 0x5211, 0x5212, 0x5213, 0x5216, 0x5218, 0x521c, 0x5f36, 0x6026, 0x6070, 0x610e, 0x610f, 0x6184, 0x61a2, 0x62bc, 0x62be, 0x6364, 0x648d, 0x649b, 0x6507, 0x6508, 0x6539, 0x653a, 0x653c:
		return rm4
	}
	return unknown
}

type device struct {
	addr   string
	mac    []byte
	typ    uint16
	family family
	gate   chan struct{}
}

type session struct {
	*device
	conn  net.Conn
	block cipher.Block
	id    uint32
	seq   uint16
}

func (d *device) use(ctx context.Context, fn func(*session) error) error {
	select {
	case d.gate <- struct{}{}:
		defer func() { <-d.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "udp", d.addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	block, _ := aes.NewCipher(initKey)
	s := &session{device: d, conn: conn, block: block, seq: uint16(rand.Uint32()) | 0x8000}
	req := make([]byte, 0x50)
	copy(req[4:], bytes.Repeat([]byte{'1'}, 16))
	req[0x1e], req[0x2d] = 1, 1
	copy(req[0x30:], "Test 1")
	body, err := s.send(0x65, req, true)
	if err != nil {
		return fmt.Errorf("Broadlink 认证: %w", err)
	}
	if len(body) < 20 {
		return errors.New("Broadlink 认证响应过短")
	}
	s.id = le.Uint32(body)
	s.block, _ = aes.NewCipher(body[4:20])
	return fn(s)
}

func checksum(b []byte) uint16 {
	sum := uint16(0xbeaf)
	for _, v := range b {
		sum += uint16(v)
	}
	return sum
}

func (s *session) send(cmd uint16, payload []byte, readOnly bool) ([]byte, error) {
	const h = 0x38
	s.seq = (s.seq + 1) | 0x8000
	plen := (len(payload) + 15) &^ 15
	if h+plen > 65507 {
		return nil, errors.New("Broadlink 报文过长")
	}
	msg := make([]byte, h+plen)
	copy(msg, magic)
	le.PutUint16(msg[0x24:], s.typ)
	le.PutUint16(msg[0x26:], cmd)
	le.PutUint16(msg[0x28:], s.seq)
	for i, c := range s.mac {
		msg[0x2f-i] = c
	}
	le.PutUint32(msg[0x30:], s.id)
	le.PutUint16(msg[0x34:], checksum(payload))
	copy(msg[h:], payload)
	cipher.NewCBCEncrypter(s.block, iv).CryptBlocks(msg[h:], msg[h:])
	le.PutUint16(msg[0x20:], checksum(msg))
	resp := make([]byte, 65536)
	for attempt := 0; ; attempt++ {
		if err := s.conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			return nil, err
		}
		if _, err := s.conn.Write(msg); err != nil {
			return nil, err
		}
		n, err := s.conn.Read(resp)
		if err != nil {
			if e, ok := err.(net.Error); readOnly && attempt < 2 && ok && e.Timeout() {
				continue
			}
			return nil, err
		}
		if n < 0x30 {
			return nil, errors.New("Broadlink 响应头过短")
		}
		reply := resp[:n]
		if checksum(reply)-uint16(reply[0x20])-uint16(reply[0x21]) != le.Uint16(reply[0x20:]) {
			return nil, errors.New("Broadlink 响应校验失败")
		}
		if code := int16(le.Uint16(reply[0x22:])); code != 0 {
			return nil, fmt.Errorf("Broadlink 设备拒绝请求（%d）", code)
		}
		if n <= h {
			return nil, nil
		}
		body := reply[h:]
		if len(body)%aes.BlockSize != 0 {
			return nil, errors.New("Broadlink 加密响应长度无效")
		}
		cipher.NewCBCDecrypter(s.block, iv).CryptBlocks(body, body)
		return body, nil
	}
}

func (s *session) power(on *bool) (bool, error) {
	state := byte(0)
	if on != nil && *on {
		state = 1
	}
	if s.family == sp1 {
		if on == nil {
			return false, errors.New("SP1 不支持查询电源")
		}
		_, err := s.send(0x66, []byte{state, 0, 0, 0}, false)
		return *on, err
	}
	if s.family == sp4 || s.family == sp4b {
		flag := byte(1)
		data := []byte(`{}`)
		if on != nil {
			flag = 2
			data = []byte(fmt.Sprintf(`{"pwr":%d}`, state))
		}
		off := 0
		if s.family == sp4b {
			off = 2
		}
		p := make([]byte, off+12)
		if off != 0 {
			le.PutUint16(p, uint16(12+len(data)))
		}
		p[off], p[off+1], p[off+2], p[off+3] = 0xa5, 0xa5, 0x5a, 0x5a
		p[off+6], p[off+7] = flag, 0x0b
		le.PutUint32(p[off+8:], uint32(len(data)))
		p = append(p, data...)
		le.PutUint16(p[off+4:], checksum(p[off:]))
		reply, err := s.send(0x6a, p, on == nil)
		if err != nil {
			return false, err
		}
		if len(reply) < off+12 {
			return false, errors.New("Broadlink 电源响应过短")
		}
		n := int(le.Uint32(reply[off+8:]))
		if n < 0 || n > len(reply)-off-12 {
			return false, errors.New("Broadlink 电源响应长度无效")
		}
		var got struct {
			Power *int `json:"pwr"`
		}
		if err := json.Unmarshal(reply[off+12:off+12+n], &got); err != nil {
			return false, err
		}
		if on != nil {
			return *on, nil
		}
		if got.Power == nil {
			return false, errors.New("Broadlink 响应缺少电源状态")
		}
		return *got.Power != 0, nil
	}
	if on != nil && s.family == sp3 {
		raw, err := s.readPower()
		if err != nil {
			return false, err
		}
		state |= raw & 2
	}
	if on != nil {
		p := make([]byte, 16)
		p[0], p[4] = 2, state
		_, err := s.send(0x6a, p, false)
		return *on, err
	}
	raw, err := s.readPower()
	if s.family == sp3 {
		raw &= 1
	}
	return raw != 0, err
}

func (s *session) readPower() (byte, error) {
	p := make([]byte, 16)
	p[0] = 1
	reply, err := s.send(0x6a, p, true)
	if err != nil {
		return 0, err
	}
	if len(reply) < 5 {
		return 0, errors.New("Broadlink 电源响应过短")
	}
	return reply[4], nil
}

func (s *session) transmit(data []byte) error {
	p := []byte{2, 0, 0, 0}
	if s.family == rm4 {
		p = binary.LittleEndian.AppendUint16(nil, uint16(len(data)+4))
		p = append(p, 2, 0, 0, 0)
	}
	_, err := s.send(0x6a, append(p, data...), false)
	return err
}
