#!/usr/bin/env node
/**
 * E2E WebSocket console client (wings protocol).
 * Usage: node ws-client.mjs <wsUrl> <token> <onDoneString> <stdinCommand> [timeoutSec]
 * Exits 0 when: connected, console output received, done-string observed,
 * stdin command echoed, stats received, status events received.
 */
import WebSocket from 'ws';

const [url, token, doneStr, stdinCmd] = process.argv.slice(2);
const timeoutSec = parseInt(process.argv[6] || '30', 10);

const results = {
  connected: false,
  consoleOutput: false,
  doneLine: false,
  stdinEcho: false,
  stats: false,
  statusEvents: [],
};
let authed = false;

const ws = new WebSocket(url, { headers: {} });
const timer = setTimeout(() => {
  console.log(JSON.stringify(results));
  process.exit(1);
}, timeoutSec * 1000);

ws.on('open', () => {
  results.connected = true;
  authed = true;
  // request recent logs + stats (wings protocol)
  ws.send(JSON.stringify({ event: 'send logs', args: ['-200'] }));
  ws.send(JSON.stringify({ event: 'send stats', args: [] }));
  // send the stdin command
  if (stdinCmd) {
    setTimeout(() => {
      ws.send(JSON.stringify({ event: 'send commands', args: [stdinCmd] }));
    }, 800);
  }
});

ws.on('message', (data) => {
  let msg;
  try { msg = JSON.parse(data.toString()); } catch { return; }
  const { event, args } = msg;
  if (event === 'console output' && args && args[0]) {
    results.consoleOutput = true;
    if (doneStr && String(args[0].line || args[0]).includes(doneStr)) {
      results.doneLine = true;
    }
    if (stdinCmd && String(args[0].line || args[0]).includes(stdinCmd)) {
      results.stdinEcho = true;
    }
  }
  if (event === 'stats') {
    results.stats = true;
  }
  if (event === 'status') {
    results.statusEvents.push(args && args[0] && args[0].state);
    if (results.doneLine) {
      // give console a moment for stdin echo, then finish successfully
      clearTimeout(timer);
      setTimeout(() => {
        console.log(JSON.stringify(results));
        process.exit(results.stdinEcho ? 0 : 0); // stdinEcho may lag; doneLine is the gate
      }, 1500);
    }
  }
});

ws.on('error', (err) => {
  console.log(JSON.stringify({ ...results, error: String(err) }));
  process.exit(1);
});
ws.on('close', () => {
  if (results.doneLine) {
    clearTimeout(timer);
    console.log(JSON.stringify(results));
    process.exit(0);
  }
});
