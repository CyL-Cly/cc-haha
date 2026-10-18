"""External transport guard shared by disposable public CLI PTY fixtures."""
import http.server
import json
import os
import socketserver
import threading

class PrivateLoopbackHTTPServer(http.server.ThreadingHTTPServer):
 def server_bind(self):
  # HTTPServer.server_bind performs a reverse-DNS lookup even for 127.0.0.1.
  # These private peers use their numeric endpoint exclusively; a resolver is
  # neither part of the product flow nor an allowed external fixture service.
  assert self.server_address[0] == '127.0.0.1', self.server_address
  socketserver.TCPServer.server_bind(self)
  self.server_name, self.server_port = self.server_address

class PrivatePublicCLINetwork:
 def __init__(self, environment):
  # Go runtime instrumentation belongs to the already-owned parent command.
  # Preserve it when this driver creates its private product environment.
  if os.environ.get('GOCOVERDIR'):
   environment['GOCOVERDIR'] = os.environ['GOCOVERDIR']
  self.attempts = []
  attempts = self.attempts
  class Guard(http.server.BaseHTTPRequestHandler):
   def reject(self):
    attempts.append({'method': self.command, 'path': self.path})
    self.send_response(403); self.end_headers()
   do_CONNECT = reject
   do_GET = reject
   do_POST = reject
   def log_message(self, *args): pass
  self.server = PrivateLoopbackHTTPServer(('127.0.0.1', 0), Guard)
  self.worker = threading.Thread(target=self.server.serve_forever, daemon=True)
  self.worker.start()
  proxy = 'http://127.0.0.1:' + str(self.server.server_address[1])
  environment['HTTPS_PROXY'] = proxy
  environment['HTTP_PROXY'] = proxy
  environment['NO_PROXY'] = '127.0.0.1,localhost'
 def assert_private(self):
  assert not self.attempts, 'unowned public CLI HTTP: ' + json.dumps(self.attempts)
 def close(self):
  self.server.shutdown(); self.server.server_close(); self.worker.join()

 def seed_startup(self, binary, environment, work, config):
  # Observe the actual built executable's version. A populated same-version
  # cache and the user's disabled auto-update setting select the real Source
  # no-download branches; neither changes the exit/history/tool consumers.
  import subprocess
  version = subprocess.check_output([str(binary), '--version'], cwd=work, env=environment, text=True, timeout=5).strip().split()[0]
  path = config / '.claude.json'
  value = json.loads(path.read_text()) if path.exists() else {}
  value.update({'lastReleaseNotesSeen': version, 'autoUpdates': False})
  path.write_text(json.dumps(value))
  (config / 'cache').mkdir(exist_ok=True)
  (config / 'cache' / 'changelog.md').write_text('## 0.0.0\n')
  return version
