package pinger

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"bug-bounty-endpoint-pinger/internal/models"
)

func TestPingSendsConfiguredRequestBodyForAnyMethod(t *testing.T) {
	requestBody := "<probe>\n  <ok>true</ok>\n</probe>"
	bodySeen := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		bodySeen <- string(data)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	p := New(2*time.Second, 1024, true, nil)
	result := p.Ping(context.Background(), models.Endpoint{
		URL:                target.URL,
		HTTPMethod:         "GET",
		RequestBodyEnabled: true,
		RequestBody:        requestBody,
	})
	if result.Error != nil {
		t.Fatalf("Ping error=%s", *result.Error)
	}
	select {
	case got := <-bodySeen:
		if got != requestBody {
			t.Fatalf("request body=%q, want %q", got, requestBody)
		}
	case <-time.After(time.Second):
		t.Fatal("target did not receive request")
	}
}

func TestPingUsesSocks5ProxyWithAuthentication(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	proxyAddress, connects := startTestSocks5Proxy(t, "user", "pass")
	p := New(2*time.Second, 1024, true, nil)
	result := p.Ping(context.Background(), models.Endpoint{
		URL:        target.URL,
		HTTPMethod: "GET",
		Proxy: models.ProxyConfig{
			Enabled:  true,
			Address:  proxyAddress,
			Username: "user",
			Password: "pass",
		},
	})
	if result.Error != nil {
		t.Fatalf("Ping error=%s", *result.Error)
	}
	if result.StatusCode == nil || *result.StatusCode != http.StatusOK {
		t.Fatalf("status=%v, want 200", result.StatusCode)
	}
	select {
	case targetAddress := <-connects:
		if targetAddress == "" {
			t.Fatal("proxy received empty target")
		}
	case <-time.After(time.Second):
		t.Fatal("proxy did not receive a CONNECT request")
	}
}

func startTestSocks5Proxy(t *testing.T, username, password string) (string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connects := make(chan string, 1)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := handleTestSocks5Conn(conn, username, password, connects); err != nil {
			t.Logf("SOCKS5 test proxy: %v", err)
		}
	}()
	return listener.Addr().String(), connects
}

func handleTestSocks5Conn(conn net.Conn, username, password string, connects chan<- string) error {
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return err
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	method := byte(0x00)
	if username != "" {
		method = 0x02
	}
	if _, err := conn.Write([]byte{0x05, method}); err != nil {
		return err
	}
	if method == 0x02 {
		if err := handleTestSocks5Auth(conn, username, password); err != nil {
			return err
		}
	}
	targetAddress, err := readTestSocks5Connect(conn)
	if err != nil {
		return err
	}
	upstream, err := net.Dial("tcp", targetAddress)
	if err != nil {
		return err
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	connects <- targetAddress
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
		done <- struct{}{}
	}()
	<-done
	return nil
}

func handleTestSocks5Auth(conn net.Conn, username, password string) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	user := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, user); err != nil {
		return err
	}
	passLen := make([]byte, 1)
	if _, err := io.ReadFull(conn, passLen); err != nil {
		return err
	}
	pass := make([]byte, int(passLen[0]))
	if _, err := io.ReadFull(conn, pass); err != nil {
		return err
	}
	status := byte(0x00)
	if string(user) != username || string(pass) != password {
		status = 0x01
	}
	_, err := conn.Write([]byte{0x01, status})
	if status != 0 {
		return fmt.Errorf("unexpected proxy credentials")
	}
	return err
}

func readTestSocks5Connect(conn net.Conn) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	if header[0] != 0x05 || header[1] != 0x01 {
		return "", fmt.Errorf("unexpected SOCKS5 command")
	}
	var host string
	switch header[3] {
	case 0x01:
		raw := make([]byte, 4)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return "", err
		}
		host = net.IP(raw).String()
	case 0x04:
		raw := make([]byte, 16)
		if _, err := io.ReadFull(conn, raw); err != nil {
			return "", err
		}
		host = net.IP(raw).String()
	case 0x03:
		rawLen := make([]byte, 1)
		if _, err := io.ReadFull(conn, rawLen); err != nil {
			return "", err
		}
		raw := make([]byte, int(rawLen[0]))
		if _, err := io.ReadFull(conn, raw); err != nil {
			return "", err
		}
		host = string(raw)
	default:
		return "", fmt.Errorf("unsupported address type")
	}
	portRaw := make([]byte, 2)
	if _, err := io.ReadFull(conn, portRaw); err != nil {
		return "", err
	}
	port := int(portRaw[0])<<8 | int(portRaw[1])
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}
