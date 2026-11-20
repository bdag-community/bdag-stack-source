// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 BlockDAG community contributors
// Package groestl implements the groestl-256 hash used by CryptoNight.
package groestl

/*
#include "sph_groestl.h"
*/
import "C"
import (
	"hash"
	"unsafe"
)

const Size = 32
const BlockSize = 64

type digest struct{ state C.sph_groestl256_context }

func New256() hash.Hash          { d := new(digest); d.Reset(); return d }
func (d *digest) Reset()         { C.sph_groestl256_init(unsafe.Pointer(&d.state)) }
func (d *digest) Size() int      { return Size }
func (d *digest) BlockSize() int { return BlockSize }
func (d *digest) Write(p []byte) (int, error) {
	if len(p) > 0 {
		C.sph_groestl256(unsafe.Pointer(&d.state), unsafe.Pointer(&p[0]), C.size_t(len(p)))
	}
	return len(p), nil
}
func (d *digest) Sum(b []byte) []byte {
	s := d.state
	var out [Size]byte
	C.sph_groestl256_close(unsafe.Pointer(&s), unsafe.Pointer(&out[0]))
	return append(b, out[:]...)
}
func Sum256(p []byte) []byte { d := New256(); _, _ = d.Write(p); return d.Sum(nil) }
