package pinger

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// decodedResponseBody handles explicit Accept-Encoding headers, for which the
// HTTP transport does not automatically decompress responses. Decoding streams
// into the caller's size limit so the limit applies to text, not compressed bytes.
func decodedResponseBody(body io.Reader, contentEncoding string) (io.Reader, func(), error) {
	var closes []func()
	cleanup := func() {
		for i := len(closes) - 1; i >= 0; i-- {
			closes[i]()
		}
	}
	encodings := strings.Split(contentEncoding, ",")
	if len(encodings) > 4 {
		return nil, cleanup, fmt.Errorf("too many response content encodings")
	}
	for i := len(encodings) - 1; i >= 0; i-- {
		encoding := strings.ToLower(strings.TrimSpace(encodings[i]))
		var err error
		switch encoding {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			var decoder *gzip.Reader
			decoder, err = gzip.NewReader(body)
			if err == nil {
				body = decoder
				closes = append(closes, func() { _ = decoder.Close() })
			}
		case "deflate":
			buffered := bufio.NewReader(body)
			prefix, _ := buffered.Peek(2)
			var decoder io.ReadCloser
			// HTTP deflate uses zlib framing, but some servers send raw DEFLATE.
			if len(prefix) == 2 && prefix[0]&0x0f == 8 && prefix[0]>>4 <= 7 && (int(prefix[0])*256+int(prefix[1]))%31 == 0 {
				decoder, err = zlib.NewReader(buffered)
			} else {
				decoder = flate.NewReader(buffered)
			}
			if err == nil {
				body = decoder
				closes = append(closes, func() { _ = decoder.Close() })
			}
		case "br":
			body = brotli.NewReader(body)
		case "zstd":
			var decoder *zstd.Decoder
			decoder, err = zstd.NewReader(body, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20), zstd.WithDecodeBuffersBelow(0))
			if err == nil {
				body = decoder
				closes = append(closes, decoder.Close)
			}
		default:
			err = fmt.Errorf("unsupported response content encoding %q", encoding)
		}
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("decode response: %w", err)
		}
	}
	return body, cleanup, nil
}
