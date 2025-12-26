// Copyright (c) 2025 Berik Ashimov

package main

import (
	"math/big"
	"net/netip"
)

func addrBitLen(a netip.Addr) int {
	if a.Is4() {
		return 32
	}
	return 128
}

func addrToBig(a netip.Addr) *big.Int {
	if a.Is4() {
		b := a.As4()
		return new(big.Int).SetBytes(b[:])
	}
	b := a.As16()
	return new(big.Int).SetBytes(b[:])
}

func bigToAddr(i *big.Int, bits int) (netip.Addr, bool) {
	if bits == 32 {
		if i.Sign() < 0 || i.BitLen() > 32 {
			return netip.Addr{}, false
		}
		buf := i.FillBytes(make([]byte, 4))
		var out [4]byte
		copy(out[:], buf)
		return netip.AddrFrom4(out), true
	}
	if i.Sign() < 0 || i.BitLen() > 128 {
		return netip.Addr{}, false
	}
	buf := i.FillBytes(make([]byte, 16))
	var out [16]byte
	copy(out[:], buf)
	return netip.AddrFrom16(out), true
}

func prefixSize(p netip.Prefix) *big.Int {
	bits := addrBitLen(p.Addr())
	sizeBits := bits - p.Bits()
	if sizeBits <= 0 {
		return big.NewInt(1)
	}
	return new(big.Int).Lsh(big.NewInt(1), uint(sizeBits))
}

func prefixLastAddr(p netip.Prefix) (netip.Addr, bool) {
	masked := p.Masked()
	start := addrToBig(masked.Addr())
	size := prefixSize(masked)
	last := new(big.Int).Sub(new(big.Int).Add(start, size), big.NewInt(1))
	return bigToAddr(last, addrBitLen(masked.Addr()))
}

func prefixWithin(pool, p netip.Prefix) bool {
	if !pool.Contains(p.Addr()) {
		return false
	}
	last, ok := prefixLastAddr(p)
	if !ok {
		return false
	}
	return pool.Contains(last)
}

func alignUp(n, step *big.Int) *big.Int {
	if step.Sign() == 0 {
		return new(big.Int).Set(n)
	}
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, step, r)
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Mul(q, step)
}
