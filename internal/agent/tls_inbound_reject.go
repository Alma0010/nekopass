package agent

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Match the TLS alert used by nginx's ssl_reject_handshake. This runs before
// ServerHello; it must never send an HTTP response into a TLS handshake.
func rejectTLSInbound(conn net.Conn) {
	_, _ = conn.Write([]byte{21, 3, 3, 0, 2, 2, 112}) // fatal unrecognized_name
}

const plainHTTPOnTLSBody = "<html>\r\n<head><title>400 The plain HTTP request was sent to HTTPS port</title></head>\r\n" +
	"<body>\r\n<center><h1>400 Bad Request</h1></center>\r\n" +
	"<center>The plain HTTP request was sent to HTTPS port</center>\r\n" +
	"<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

const badHTTPRequestBody = "<html>\r\n<head><title>400 Bad Request</title></head>\r\n" +
	"<body>\r\n<center><h1>400 Bad Request</h1></center>\r\n" +
	"<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

const largeHTTPHeaderBody = "<html>\r\n<head><title>400 Request Header Or Cookie Too Large</title></head>\r\n" +
	"<body>\r\n<center><h1>400 Bad Request</h1></center>\r\n" +
	"<center>Request Header Or Cookie Too Large</center>\r\n" +
	"<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

const largeHTTPURIBody = "<html>\r\n<head><title>414 Request-URI Too Large</title></head>\r\n" +
	"<body>\r\n<center><h1>414 Request-URI Too Large</h1></center>\r\n" +
	"<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

const unsupportedHTTPVersionBody = "<html>\r\n<head><title>505 HTTP Version Not Supported</title></head>\r\n" +
	"<body>\r\n<center><h1>505 HTTP Version Not Supported</h1></center>\r\n" +
	"<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

const (
	plainHTTPLineLimit   = 8 * 1024
	plainHTTPHeaderLimit = 4 * plainHTTPLineLimit
)

var errPlainHTTPTooLarge = errors.New("plaintext HTTP header too large")

// Apply nginx's default 8 KiB line limit within a bounded 32 KiB budget.
// The caller's ClientHello deadline and concurrency limits remain in force.
func readPlainHTTPLine(reader *bufio.Reader, remaining *int) (string, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		*remaining -= len(fragment)
		if *remaining < 0 || len(line)+len(fragment) >= plainHTTPLineLimit {
			return string(line), errPlainHTTPTooLarge
		}
		line = append(line, fragment...)
		if err != bufio.ErrBufferFull {
			return string(line), err
		}
	}
}

func rejectPlainHTTPOnTLS(conn net.Conn, reader *bufio.Reader) {
	remaining := plainHTTPHeaderLimit
	line, err := readPlainHTTPLine(reader, &remaining)
	head := strings.HasPrefix(line, "HEAD ")
	if errors.Is(err, errPlainHTTPTooLarge) {
		writePlainHTTPRejection(conn, "414 Request-URI Too Large", largeHTTPURIBody, head)
		return
	}
	if err != nil {
		return
	}
	badRequest := func() { writePlainHTTPRejection(conn, "400 Bad Request", badHTTPRequestBody, head) }
	parts := strings.Split(strings.TrimRight(line, "\r\n"), " ")
	if len(parts) != 3 || !plainHTTPMethod(parts[0]) {
		badRequest()
		return
	}
	major, minor, valid := http.ParseHTTPVersion(parts[2])
	if !valid || strings.TrimRight(line, "\r\n") == "PRI * HTTP/2.0" {
		badRequest()
		return
	}
	if major > 1 {
		writePlainHTTPRejection(conn, "505 HTTP Version Not Supported", unsupportedHTTPVersionBody, head)
		return
	}
	var normalized strings.Builder
	normalized.WriteString(line)
	counts := make(map[string]int)
	for {
		line, err = readPlainHTTPLine(reader, &remaining)
		if errors.Is(err, errPlainHTTPTooLarge) {
			writePlainHTTPRejection(conn, "400 Bad Request", largeHTTPHeaderBody, head)
			return
		}
		if err != nil {
			return
		}
		value := strings.TrimRight(line, "\r\n")
		if value == "" {
			normalized.WriteString("\r\n")
			break
		}
		name, _, hasColon := strings.Cut(value, ":")
		if strings.ContainsAny(name, " \t") {
			badRequest()
			return
		}
		name = strings.ToLower(name)
		counts[name]++
		if (name == "host" || name == "content-length" || name == "transfer-encoding") && counts[name] > 1 {
			badRequest()
			return
		}
		// nginx accepts a header name without a colon as an empty value.
		// Preserve it for validation instead of letting Go reject it outright.
		if !hasColon {
			normalized.WriteString(value + ":\r\n")
		} else {
			normalized.WriteString(line)
		}
	}
	request, err := http.ReadRequest(bufio.NewReader(strings.NewReader(normalized.String())))
	if err != nil || major != 1 || minor > 1 || (minor == 1 && counts["host"] == 0) ||
		(counts["host"] > 0 && (request.Host == "" || strings.ContainsAny(request.Host, " /\\") || strings.Contains(request.Host, ".."))) ||
		(counts["content-length"] > 0 && counts["transfer-encoding"] > 0) {
		badRequest()
		return
	}
	writePlainHTTPRejection(conn, "400 Bad Request", plainHTTPOnTLSBody, head)
}

func plainHTTPMethod(method string) bool {
	if method == "" {
		return false
	}
	for _, ch := range method {
		if (ch < 'A' || ch > 'Z') && ch != '_' && ch != '-' {
			return false
		}
	}
	return true
}

func writePlainHTTPRejection(conn net.Conn, status, body string, head bool) {
	response := fmt.Sprintf("HTTP/1.1 %s\r\nServer: nginx\r\nDate: %s\r\nContent-Type: text/html\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", status, time.Now().UTC().Format(http.TimeFormat), len(body))
	if !head {
		response += body
	}
	// No request body is consumed: the caller closes the entire connection.
	_, _ = io.WriteString(conn, response)
}
