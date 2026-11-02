"""Disposable protocol participant for image/browser tests; never production code."""
import base64
import hashlib
import hmac
import http.client
import http.server
import json
import os
from pathlib import Path
import socket
import socketserver
import sys

CHANNEL = Path('/run/nox-suite')
STATE = Path('/fixture/participant-state.json')
RECEIPT = Path('/fixture/participant-receipt.json')
DECLARATION = {'contractVersion': '1.0', 'productId': 'nox-yard', 'productVersion': 'fixture', 'displayName': 'Local protocol participant', 'capabilities': {}}


class LocalConnection(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(10)
        self.sock.connect(str(CHANNEL / 'nox-backend.sock'))


def call(method, path, value=None, credential=None, key=None):
    connection = LocalConnection('nox-backend', timeout=10)
    headers = {'Content-Type': 'application/json'}
    if credential:
        headers['Authorization'] = 'Bearer ' + credential
    if key:
        headers['Idempotency-Key'] = key
    try:
        connection.request(method, path, json.dumps(value).encode() if value is not None else None, headers)
        response = connection.getresponse()
        raw = response.read(2 * 1024 * 1024)
        if response.status not in (200, 201, 204):
            raise RuntimeError('Backend protocol response status: ' + str(response.status))
        return json.loads(raw) if raw else None
    finally:
        connection.close()


def private_json(path, value):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, 'w') as stream:
        json.dump(value, stream)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()


def url64(value):
    return base64.urlsafe_b64encode(value).decode().rstrip('=')


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass  # Never print pairing payloads or credentials.

    def do_GET(self):
        if self.path != '/nox/integration':
            self.send_error(404)
            return
        body = json.dumps(DECLARATION).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        if self.path != '/nox/integration/pairing':
            self.send_error(404)
            return
        try:
            size = int(self.headers.get('Content-Length', '0'))
            if not 0 < size <= 2 * 1024 * 1024:
                raise ValueError('invalid body size')
            envelope = json.loads(self.rfile.read(size))
            intent = envelope['intent']
            if envelope['backendOrigin'] != 'http://nox-backend' or intent['productId'] != 'nox-yard' or intent['baseUrl'] != 'http://nox-yard':
                raise ValueError('unexpected binding')
            state = json.loads(STATE.read_text()) if STATE.exists() else {'deliveries': 0}
            state['deliveries'] += 1
            private_json(STATE, state)
            receipt = json.loads(RECEIPT.read_text()) if RECEIPT.exists() else {}
            if receipt.get('pairingId') != intent['pairingId']:
                binding = {'contractVersion': '1.0', 'pairingId': intent['pairingId'], 'nonce': intent['nonce'], 'productId': intent['productId'], 'baseUrl': intent['baseUrl'], 'declarationFingerprint': url64(hashlib.sha256(canonical(DECLARATION)).digest())}
                secret = base64.urlsafe_b64decode(intent['secret'] + '=' * (-len(intent['secret']) % 4))
                proof = {'contractVersion': '1.0', 'binding': binding, 'proof': url64(hmac.new(secret, canonical(binding), hashlib.sha256).digest())}
                provisioning = call('POST', '/api/nox/pairings/' + intent['pairingId'] + '/complete', proof, key=intent['pairingId'] + ':complete')
                receipt = {'pairingId': intent['pairingId'], 'credential': provisioning['serviceCredential'], 'acknowledged': False}
                private_json(RECEIPT, receipt)  # Persistence must precede ACK.
            if not receipt['acknowledged']:
                call('POST', '/api/nox/pairings/' + intent['pairingId'] + '/ack', credential=receipt['credential'], key=intent['pairingId'] + ':ack')
                receipt['acknowledged'] = True
                private_json(RECEIPT, receipt)
            self.send_response(204)
            self.end_headers()
        except Exception:
            self.send_error(503, 'Local protocol fixture could not complete pairing')


class LocalServer(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


if __name__ == '__main__':
    if sys.argv[1:] == ['--keys']:
        print(json.dumps(call('GET', '/api/nox/keys')))
    else:
        path = CHANNEL / 'nox-yard.sock'
        with LocalServer(str(path), Handler) as server:
            os.chmod(path, 0o660)
            print('Local protocol fixture ready', flush=True)
            server.serve_forever()
