'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { cookieHeader } = require('../../app/auth/cookies.cjs');

test('redirect cookies replace early login cookies without losing equals signs', () => {
  assert.equal(cookieHeader([
    { name: 'BDUSS', value: 'latest==', domain: '.baidu.com', expires: -1 },
  ], 'pan.baidu.com', ['BDUSS=old', 'STOKEN=fallback==']), 'BDUSS=latest==; STOKEN=fallback==');
});

test('host cookie wins regardless of jar order; other domains never enter credentials', () => {
  assert.equal(cookieHeader([
    { name: 'BDUSS', value: 'host', domain: 'pan.baidu.com', expires: -1 },
    { name: 'BDUSS', value: 'parent', domain: '.baidu.com', expires: -1 },
    { name: 'unrelated', value: 'private', domain: 'notbaidu.com', expires: -1 },
  ], 'pan.baidu.com'), 'BDUSS=host');
});

test('expired or deleted cookies cannot be resurrected by an earlier response', () => {
  assert.equal(cookieHeader([
    { name: 'BDUSS', value: 'expired', domain: '.baidu.com', expires: 10 },
    { name: 'STOKEN', value: '', domain: '.baidu.com', expires: -1 },
  ], 'pan.baidu.com', ['BDUSS=old', 'STOKEN=old'], 20), '');
});

test('Quark cookies still use matching parent domains and preserve session cookies', () => {
  assert.equal(cookieHeader([
    { name: '__puus', value: 'session', domain: '.quark.cn', expires: -1 },
    { name: '__pus', value: 'valid', domain: 'drive-pc.quark.cn', expires: 100 },
  ], 'drive-pc.quark.cn', [], 20), '__puus=session; __pus=valid');
});
