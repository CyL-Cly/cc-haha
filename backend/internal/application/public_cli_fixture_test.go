package application

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPrivatePublicCLILoopbackPeersNeverResolveDNS(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-c", `
import http.client, http.server, socket, sys, threading
sys.path.insert(0, 'testdata')
from public_cli_fixture import PrivateLoopbackHTTPServer, PrivatePublicCLINetwork
lookups = []
def forbidden(*args):
 lookups.append(args)
 raise AssertionError('fixture attempted reverse DNS')
socket.getfqdn = forbidden
socket.gethostbyaddr = forbidden
class Handler(http.server.BaseHTTPRequestHandler):
 def do_POST(self):
  body = self.rfile.read(int(self.headers['Content-Length']))
  assert body == b'actual private socket'
  self.send_response(200); self.end_headers(); self.wfile.write(b'owned')
 def log_message(self, *args): pass
peer = PrivateLoopbackHTTPServer(('127.0.0.1', 0), Handler)
worker = threading.Thread(target=peer.serve_forever, daemon=True); worker.start()
env = {}
network = PrivatePublicCLINetwork(env)
try:
 assert peer.server_name == '127.0.0.1' and peer.server_port == peer.server_address[1]
 client = http.client.HTTPConnection('127.0.0.1', peer.server_port, timeout=2)
 client.request('POST', '/actual', b'actual private socket')
 response = client.getresponse()
 assert response.status == 200 and response.read() == b'owned'
 client.close()
 network.assert_private()
 proxy = http.client.HTTPConnection('127.0.0.1', network.server.server_port, timeout=2)
 proxy.request('CONNECT', 'forbidden.invalid:443')
 response = proxy.getresponse()
 assert response.status == 403
 response.read(); proxy.close()
 assert network.attempts == [{'method':'CONNECT','path':'forbidden.invalid:443'}]
 assert lookups == []
 print('actualLoopbackHTTP=owned proxyReject=403 reverseDNS=0')
finally:
 peer.shutdown(); peer.server_close(); worker.join()
 network.close()
`)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "actualLoopbackHTTP=owned proxyReject=403 reverseDNS=0") {
		t.Fatalf("private public-CLI transport fixture: %v %s", err, out)
	}
}
