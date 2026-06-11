<?php
/**
 * 认证哈希函数 — 给定原文字符串，返回哈希字符串
 *
 * 唯一职责：哈希运算。不知道调用方是谁，不知道结果用来做什么。
 * 零依赖（不 require 任何文件），是依赖链的底端。
 *
 * 换哈希算法时：只改这一个文件。
 */

// @关键_$27：auth_hash — 认证哈希函数（SHA-256），换算法只改此文件，零依赖
function auth_hash(string $raw): string {
    return hash('sha256', $raw);
}
