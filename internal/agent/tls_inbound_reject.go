package agent

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
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

// Only parsed plaintext HTTP receives a banner. The caller's ClientHello
// deadline and concurrency limits also apply to this bounded header read.
func rejectPlainHTTPOnTLS(conn net.Conn, reader *bufio.Reader) {
	request, err := http.ReadRequest(bufio.NewReader(io.LimitReader(reader, 16*1024)))
	if err != nil || request.ProtoMajor != 1 || request.ProtoMinor > 1 {
		return
	}
	response := fmt.Sprintf("HTTP/1.1 400 Bad Request\r\nServer: nginx\r\nDate: %s\r\nContent-Type: text/html\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", time.Now().UTC().Format(http.TimeFormat), len(plainHTTPOnTLSBody))
	if request.Method != http.MethodHead {
		response += plainHTTPOnTLSBody
	}
	// No request body is consumed: the caller closes the entire connection.
	_, _ = io.WriteString(conn, response)
}
