package pinger

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

type socks5Dialer struct {
	proxyAddress string
	username     string
	password     string
	timeout      time.Duration
}

func (d socks5Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: d.timeout}
	conn, err := dialer.DialContext(ctx, network, d.proxyAddress)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else if d.timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(d.timeout))
	}
	if err := d.handshake(conn, address); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (d socks5Dialer) handshake(conn net.Conn, target string) error {
	methods := []byte{0x00}
	if d.username != "" {
		methods = []byte{0x00, 0x02}
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(conn, response); err != nil {
		return err
	}
	if response[0] != 0x05 {
		return fmt.Errorf("SOCKS5 proxy returned invalid greeting")
	}
	switch response[1] {
	case 0x00:
	case 0x02:
		if err := d.authenticate(conn); err != nil {
			return err
		}
	case 0xff:
		return fmt.Errorf("SOCKS5 proxy rejected authentication methods")
	default:
		return fmt.Errorf("SOCKS5 proxy selected unsupported authentication method")
	}
	if _, err := conn.Write(connectRequest(target)); err != nil {
		return err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("SOCKS5 proxy returned invalid connect response")
	}
	if err := discardSocksAddress(conn, header[3]); err != nil {
		return err
	}
	if header[1] != 0x00 {
		return fmt.Errorf("SOCKS5 proxy connect failed: %s", socksReply(header[1]))
	}
	return nil
}

func (d socks5Dialer) authenticate(conn net.Conn) error {
	username := []byte(d.username)
	password := []byte(d.password)
	if len(username) > 255 || len(password) > 255 {
		return fmt.Errorf("SOCKS5 proxy credentials are too long")
	}
	request := []byte{0x01, byte(len(username))}
	request = append(request, username...)
	request = append(request, byte(len(password)))
	request = append(request, password...)
	if _, err := conn.Write(request); err != nil {
		return err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(conn, response); err != nil {
		return err
	}
	if response[0] != 0x01 || response[1] != 0x00 {
		return fmt.Errorf("SOCKS5 proxy authentication failed")
	}
	return nil
}

func connectRequest(target string) []byte {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return []byte{0x05, 0x01, 0x00, 0x03, 0x00, 0x00, 0x00}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		port = 0
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 0x01)
			request = append(request, ip4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		hostBytes := []byte(host)
		if len(hostBytes) > 255 {
			hostBytes = hostBytes[:255]
		}
		request = append(request, 0x03, byte(len(hostBytes)))
		request = append(request, hostBytes...)
	}
	return append(request, byte(port>>8), byte(port))
}

func discardSocksAddress(reader io.Reader, atyp byte) error {
	switch atyp {
	case 0x01:
		_, err := io.CopyN(io.Discard, reader, 6)
		return err
	case 0x04:
		_, err := io.CopyN(io.Discard, reader, 18)
		return err
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return err
		}
		_, err := io.CopyN(io.Discard, reader, int64(length[0])+2)
		return err
	default:
		return fmt.Errorf("SOCKS5 proxy returned unsupported address type")
	}
}

func socksReply(reply byte) string {
	switch reply {
	case 0x01:
		return "general failure"
	case 0x02:
		return "connection not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return "unknown error"
	}
}
