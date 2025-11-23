// TypeScript WebSocket micro-benchmark
// Usage:
//   cd crossbench/ts
//   npm install ws fast-json-stringify --save
//   npx ts-node bench_ws.ts --iters 10000 --payload 1024

import { Server as WSServer } from 'ws';
import * as http from 'http';
import stringify from 'fast-json-stringify';
import * as yargs from 'yargs';

const argv = yargs
  .option('iters', { type: 'number', default: 10000 })
  .option('payload', { type: 'number', default: 1024 })
  .option('use-sdk', { type: 'boolean', default: false })
  .argv as any;

function getMessageSchema(payloadSize: number) {
  return {
    title: 'Msg',
    type: 'object',
    properties: {
      id: { type: 'integer' },
      method: { type: 'string' },
      params: {
        type: 'object',
        properties: {
          data: { type: 'string' }
        }
      }
    }
  } as const;
}

async function run() {
  const iters: number = argv.iters;
  const payload: number = argv.payload;

  // Create an HTTP server and upgrade to a ws server so we exercise the same
  // upgrade/framing code as the Go bench.
  const server = http.createServer();
  const wss = new WSServer({ server });

  wss.on('connection', (ws) => {
    ws.on('message', (msg) => {
      // echo
      ws.send(msg);
    });
  });

  // Listen on ephemeral port
  await new Promise<void>((res) => server.listen(0, '127.0.0.1', res));
  const addr = server.address() as any;
  const port = addr.port;
  const url = `ws://127.0.0.1:${port}`;

  // Prepare encoder
  let encoder: (o: any) => string;
  if (argv['use-sdk']) {
    console.log('Installing typescript-sdk from GitHub (npm)...');
    const { spawnSync } = require('child_process');
    const install = spawnSync('npm', ['install', 'github:modelcontextprotocol/typescript-sdk'], { stdio: 'inherit' });
    if (install.status !== 0) {
      console.warn('Failed to install typescript-sdk; falling back to fast-json-stringify/JSON');
    } else {
      console.log('Installed typescript-sdk (will attempt to require it)');
    }
  }

  try {
    const s = stringify(getMessageSchema(payload));
    encoder = (o: any) => s(o);
  } catch (e) {
    encoder = JSON.stringify;
  }

  // Create client using ws
  const WebSocket = require('ws');
  const client = new WebSocket(url);

  await new Promise<void>((resolve) => client.on('open', resolve));

  const payloadStr = 'x'.repeat(payload);
  const msgObj = { id: 1, method: 'test', params: { data: payloadStr } };
  const encoded = encoder(msgObj);

  // Warm up
  client.send(encoded);
  await new Promise<void>((resolve) => client.once('message', () => resolve()));

  let completed = 0;
  let recvResolve: (() => void) | null = null;
  client.on('message', () => {
    completed++;
    if (recvResolve) {
      recvResolve();
      recvResolve = null;
    }
  });

  // Run iterations
  const t0 = process.hrtime.bigint();
  for (let i = 0; i < iters; i++) {
    client.send(encoder(msgObj));
    // wait for a single message to be received
    if (completed <= i) {
      await new Promise<void>((res) => { recvResolve = res; });
    }
  }
  const t1 = process.hrtime.bigint();

  const totalNs = Number(t1 - t0);
  const nsPer = totalNs / iters;
  const opsPerSec = 1e9 * iters / totalNs;
  const bytesPer = Buffer.byteLength(encoder(msgObj), 'utf8');

  console.log(`TS microbench: iters=${iters} payload=${payload} bytes/op=${bytesPer}`);
  console.log(`  total time: ${(totalNs/1e9).toFixed(4)}s, ns/op: ${Math.round(nsPer)}, ops/s: ${Math.round(opsPerSec)}`);

  client.close();
  wss.close();
  server.close();
}

run().catch((e) => {
  console.error(e);
  process.exit(1);
});
