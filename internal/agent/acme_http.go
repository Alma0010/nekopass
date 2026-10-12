package agent

import (
	pb "github.com/nekopass/nekopass/internal/protocol"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func acmeEndpoints(node *pb.NodeConfig) map[int32][]*pb.ACMEChallenge {
	result := map[int32][]*pb.ACMEChallenge{}
	if node == nil {
		return result
	}
	if node.Tls != nil && len(node.Tls.Challenges) > 0 {
		result[node.Tls.ChallengePort] = append(result[node.Tls.ChallengePort], node.Tls.Challenges...)
	}
	for _, endpoint := range node.AcmeHttp {
		if len(endpoint.Challenges) > 0 {
			result[endpoint.Port] = append(result[endpoint.Port], endpoint.Challenges...)
		}
	}
	return result
}
func (e *Engine) configureACME(c *pb.ControlMessage) error {
	if e.challengeServers == nil {
		e.challengeServers = map[int32]*http.Server{}
	}
	endpoints := acmeEndpoints(c.Node)
	for port, server := range e.challengeServers {
		if len(endpoints[port]) == 0 {
			server.Close()
			delete(e.challengeServers, port)
		}
	}
	var ack int64
	for port, challenges := range endpoints {
		if port < 1 || port > 65535 {
			return net.InvalidAddrError("invalid certificate validation port")
		}
		if e.challengeServers[port] == nil {
			ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(int(port))))
			if err != nil {
				return err
			}
			server := &http.Server{Addr: ln.Addr().String(), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.handleACMEPort(port, w, r) }), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
			e.challengeServers[port] = server
			e.wg.Add(1)
			go func() { defer e.wg.Done(); server.Serve(ln) }()
		}
		for _, challenge := range challenges {
			ack = max(ack, challenge.Revision)
		}
	}
	e.acmeAck.Store(ack)
	return nil
}
func (e *Engine) handleACME(w http.ResponseWriter, r *http.Request) {
	node := e.node.Load()
	if node == nil || node.Tls == nil {
		http.NotFound(w, r)
		return
	}
	e.handleACMEPort(node.Tls.ChallengePort, w, r)
}
func (e *Engine) handleACMEPort(port int32, w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return
	}
	host := r.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	for _, challenge := range acmeEndpoints(e.node.Load())[port] {
		if strings.EqualFold(host, challenge.Domain) && challenge.ExpiresUnix > time.Now().Unix() && r.URL.Path == "/.well-known/acme-challenge/"+challenge.Token {
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, challenge.KeyAuthorization)
			return
		}
	}
	http.NotFound(w, r)
}
