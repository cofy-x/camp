import { test } from 'node:test';
import assert from 'node:assert/strict';
import { greet } from './greet.js';

test('greets the specified name', () => {
  assert.equal(greet('Camp'), 'Hello, Camp!');
});
