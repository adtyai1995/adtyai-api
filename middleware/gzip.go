package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/constant"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
)

const maxZstdDecoderWindow uint64 = 8 << 20 // 8 MiB power-of-two cap (R9-01)

func zstdWindowForLimit(maxBytes int64) uint64 {
	limit := uint64(maxBytes)
	if limit >= maxZstdDecoderWindow {
		return maxZstdDecoderWindow
	}
	w := uint64(1 << 10)
	for (w << 1) <= limit {
		w <<= 1
	}
	return w
}

// lazyDecompressReadCloser defers decoder construction until the first Read so
// unconsumed bodies (e.g. rejected/early-aborted requests) never spawn zstd
// goroutines or parse gzip headers, and Close always cleans up both decoder
// and underlying compressed body (R9-01).
type lazyDecompressReadCloser struct {
	encoding  string
	orig      io.ReadCloser
	maxBytes  int64
	mu        sync.Mutex
	closed    bool
	initDone  bool
	initErr   error
	reader    io.Reader
	closeFn   func() error
}

func (l *lazyDecompressReadCloser) initLocked() error {
	if l.initDone {
		return l.initErr
	}
	l.initDone = true
	switch l.encoding {
	case "gzip":
		gr, err := gzip.NewReader(l.orig)
		if err != nil {
			l.initErr = err
			return err
		}
		l.reader = gr
		l.closeFn = gr.Close
	case "br":
		l.reader = brotli.NewReader(l.orig)
	case "zstd":
		zr, err := zstd.NewReader(
			l.orig,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxWindow(zstdWindowForLimit(l.maxBytes)),
		)
		if err != nil {
			l.initErr = err
			return err
		}
		l.reader = zr
		l.closeFn = func() error {
			zr.Close()
			return nil
		}
	default:
		l.reader = l.orig
	}
	return nil
}

func (l *lazyDecompressReadCloser) Read(p []byte) (int, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return 0, io.EOF
	}
	if err := l.initLocked(); err != nil {
		l.mu.Unlock()
		return 0, err
	}
	r := l.reader
	l.mu.Unlock()
	return r.Read(p)
}

func (l *lazyDecompressReadCloser) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	closeFn := l.closeFn
	orig := l.orig
	l.mu.Unlock()

	if closeFn != nil {
		_ = closeFn()
	}
	if orig != nil {
		return orig.Close()
	}
	return nil
}

func DecompressRequestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		maxMB := constant.MaxRequestBodyMB
		if maxMB <= 0 {
			maxMB = 32
		}
		maxBytes := int64(maxMB) << 20

		origBody := c.Request.Body
		wrapMaxBytes := func(body io.ReadCloser) io.ReadCloser {
			return http.MaxBytesReader(c.Writer, body, maxBytes)
		}

		encoding := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Encoding")))
		switch encoding {
		case "gzip", "br", "zstd":
			// R9-01: Bound the compressed stream before decoding (blocking gzip
			// header OOM and zstd skippable-frame floods), initialize lazily,
			// bound decompressed output, and always Close on request return.
			limitedOrig := http.MaxBytesReader(c.Writer, origBody, maxBytes)
			lazy := &lazyDecompressReadCloser{
				encoding: encoding,
				orig:     limitedOrig,
				maxBytes: maxBytes,
			}
			defer func() { _ = lazy.Close() }()
			c.Request.Body = wrapMaxBytes(lazy)
			c.Request.Header.Del("Content-Encoding")
			c.Request.ContentLength = -1
		default:
			// Even for uncompressed bodies, enforce a max size to avoid huge request allocations.
			c.Request.Body = wrapMaxBytes(origBody)
		}

		// Continue processing the request
		c.Next()
	}
}
