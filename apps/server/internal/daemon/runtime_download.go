package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/releaseinfo"
)

var errPublicRuntimeRedirect = errors.New("GitHub runtime redirect rejected")
var errRuntimeDownloadIdle = errors.New("runtime download idle timeout")

// Only controlled classifications cross the public download boundary. In
// particular, url.Error and proxy/TLS errors can contain signed URLs or secrets.
func publicRuntimeFailure(err error) error {
	kind, next := "network", "Check network and proxy access to GitHub release services."
	var dns *net.DNSError
	var operation *net.OpError
	var certificate *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var timeout net.Error
	var status *httpError
	switch {
	case errors.Is(err, errPublicRuntimeRedirect):
		kind, next = "redirect", "The redirect violated the fixed GitHub release policy; check the release URL and any network interceptor."
	case errors.Is(err, errRuntimeDownloadIdle):
		kind, next = "idle_timeout", "The transfer stopped making progress; check network and proxy stability."
	case errors.Is(err, context.Canceled):
		kind, next = "cancelled", "The download was cancelled."
	case errors.Is(err, context.DeadlineExceeded):
		kind, next = "timeout", "The download timed out; check network and proxy availability."
	case errors.As(err, &dns):
		kind, next = "dns", "Name resolution failed; check DNS and proxy configuration."
	case errors.As(err, &certificate), errors.As(err, &authority), errors.As(err, &hostname), errors.As(err, &invalid), errors.As(err, &record):
		kind, next = "tls", "TLS verification failed; check the system clock and trusted CA certificates. Keep certificate verification enabled."
	case errors.As(err, &timeout) && timeout.Timeout():
		kind, next = "timeout", "The connection or response timed out; check network and proxy availability."
	case errors.As(err, &operation):
		kind, next = "connection", "The connection failed; check network access and proxy reachability."
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		kind, next = "interrupted", "The response ended before the pinned archive was complete; check network stability."
	case errors.As(err, &status):
		kind, next = "http", fmt.Sprintf("The release service returned HTTP %d; check public asset availability and network policy.", status.Status)
	}
	return fmt.Errorf("GitHub runtime download failed [%s]. %s Repeat setup with the same private data directory to resume", kind, next)
}

// DownloadRuntime fetches one installer-pinned artifact from the enrolled Camp
// origin. A partial transfer can resume; the caller verifies the complete hash
// before extracting or executing any bytes.
func DownloadRuntime(ctx context.Context, config Config, digest string, size int64, file *os.File, progress func(int64)) error {
	if !releaseinfo.ValidDigest(digest) || size <= 0 || size >= releaseinfo.MaxArtifactBytes {
		return errors.New("Invalid pinned runtime download")
	}
	client, err := httpClient(config)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	target, err := endpoint(config.ServerURL, "/api/daemon/runtime/"+digest)
	if err != nil {
		return err
	}
	return downloadRuntime(ctx, client, target, config.Credential, size, file, progress)
}

// Public runtime downloads have no access to Camp credentials or private CA settings.
func DownloadPublicRuntime(ctx context.Context, version, digest string, size int64, file *os.File, progress func(int64)) error {
	if !releaseinfo.ValidDigest(digest) || size <= 0 || size >= releaseinfo.MaxArtifactBytes {
		return errors.New("Invalid pinned runtime download")
	}
	platform, ok := releaseinfo.HostPlatform()
	if !ok {
		return errors.New("No native runtime is available for this platform")
	}
	target, err := releaseinfo.DownloadURL(version, releaseinfo.RuntimeFor(platform.ID, digest))
	if err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 20 * time.Second
	defer transport.CloseIdleConnections()
	return downloadRuntime(ctx, publicRuntimeClient(transport), target, "", size, file, progress)
}

func publicRuntimeClient(transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: transport, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || request.URL.Scheme != "https" || request.URL.User != nil || (request.URL.Port() != "" && request.URL.Port() != "443") {
			return errPublicRuntimeRedirect
		}
		switch request.URL.Hostname() {
		case "github.com":
			if request.URL.Path != via[0].URL.Path {
				return errPublicRuntimeRedirect
			}
		case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		default:
			return errPublicRuntimeRedirect
		}
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Origin"} {
			request.Header.Del(name)
		}
		return nil
	}}
}

func downloadRuntime(ctx context.Context, client *http.Client, target, credential string, size int64, file *os.File, progress func(int64)) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	offset := info.Size()
	if offset > size {
		if err := file.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if offset == size {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	ctx, cancelIdle := context.WithCancelCause(ctx)
	defer cancelIdle(nil)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.Do(request)
	if err != nil {
		if credential == "" {
			return publicRuntimeFailure(err)
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		if credential == "" {
			return publicRuntimeFailure(&httpError{Status: response.StatusCode})
		}
		return &httpError{Status: response.StatusCode}
	}
	if response.StatusCode == http.StatusPartialContent {
		if response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, size-1, size) {
			return errors.New("The runtime download returned an unexpected byte range")
		}
	} else if offset != 0 {
		if err := file.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if response.ContentLength >= 0 && response.ContentLength != size-offset {
		return errors.New("The runtime download has an unexpected size")
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	idle := time.AfterFunc(time.Minute, func() { cancelIdle(errRuntimeDownloadIdle) })
	defer idle.Stop()
	buffer := make([]byte, 128<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			idle.Reset(time.Minute)
			if int64(n) > size-offset {
				return errors.New("The runtime download exceeds its pinned size")
			}
			written, writeErr := file.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			offset += int64(n)
			if progress != nil {
				progress(offset)
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				if credential == "" {
					if cause := context.Cause(ctx); cause != nil {
						readErr = cause
					}
					return publicRuntimeFailure(readErr)
				}
				return readErr
			}
			if offset != size {
				if credential == "" {
					return publicRuntimeFailure(io.ErrUnexpectedEOF)
				}
				return io.ErrUnexpectedEOF
			}
			return file.Sync()
		}
	}
}
