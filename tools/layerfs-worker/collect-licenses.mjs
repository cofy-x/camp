import { collectRustLicenses } from './rust.mjs';
import process from 'node:process';

await collectRustLicenses({
  source: '/source',
  environment: process.env,
  rust: '1.85.1',
  platform: 'aarch64-unknown-linux-gnu',
  destination: '/licenses/rust',
});
