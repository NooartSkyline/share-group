'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const Logic = require('../logic.js');

test('filterAndSortFiles searches name and device then sorts', () => {
  const files = [
    { id: '1', dir: '', name: 'a.jpg', uploaderName: 'Pixel', createdAt: '2026-10-22T10:00:00Z' },
    { id: '2', dir: 'docs', name: 'note.txt', uploaderName: 'MacBook', createdAt: '2026-10-21T10:00:00Z' },
    { id: '3', dir: '', name: 'b.jpg', uploaderName: 'Pixel', createdAt: '2026-10-22T12:00:00Z' },
  ];
  const pix = Logic.filterAndSortFiles(files, 'pixel', 'newest');
  assert.deepEqual(pix.map((f) => f.id), ['3', '1']);

  const byDevice = Logic.filterAndSortFiles(files, '', 'device');
  assert.equal(byDevice[0].uploaderName, 'MacBook');
  assert.equal(Logic.fileGroupKey(byDevice[0], 'device'), 'MacBook');
  assert.equal(Logic.fileGroupKey(files[0], 'date'), '2026-10-22');
});

test('highlightCode tags keywords strings and comments without HTML', () => {
  const tokens = Logic.highlightCode('func hello() { return "x" } // hi', 'go');
  const joined = tokens.map((t) => t.text).join('');
  assert.equal(joined, 'func hello() { return "x" } // hi');
  const kinds = Object.fromEntries(tokens.filter((t) => t.cls).map((t) => [t.text, t.cls]));
  assert.equal(kinds.func, 'kw');
  assert.equal(kinds.return, 'kw');
  assert.equal(kinds['"x"'], 'st');
  assert.equal(kinds['// hi'], 'cm');
  for (const t of tokens) {
    assert.equal(t.text.includes('<'), false);
  }
});
