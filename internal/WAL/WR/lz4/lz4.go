// Package lz4 provides a minimal pure-Go LZ4 block-format codec
// for WAL record compression (REQ000034).
// LZ4 block format
// (https://github.com/lz4/lz4/blob/dev/doc/lz4_Block_format.md):
//   - The output is a sequence of "sequences"
//   - Each sequence starts with a 1-byte token: high 4 bits =
//     literal length, low 4 bits = match length
//   - If literal length is 15, additional bytes are read (each
//     value 0-254 adds to the count, 255 adds 15 and continues)
//   - Followed by [literal length bytes of literal data]
//   - Followed by a 2-byte little-endian offset (back-reference to
//     already-decoded data)
//   - If match length is 15, additional bytes are read similarly
//   - Then the next sequence begins
//   - The LAST sequence in a block has no match (no offset, no
//     match length bytes) — it's just a trailing literal run
//
// This implementation supports:
//   - Decompression: full LZ4 block format, any size
//   - Compression: greedy hash-chain match finder, sufficient for
//     WAL data
//
// The codec is intentionally minimal — no frame format, no
// checksums (WAL CRC handles that), no streaming. It compresses/
// decompresses in-memory byte slices.
package lz4

import (
	"errors"
	"fmt"
)

// Errors.
var (
	ErrMalformedInput   = errors.New("lz4: malformed input")
	ErrOffsetOutOfRange = errors.New("lz4: match offset out of range")
	ErrInputTooShort    = errors.New("lz4: input too short")
)

// minMatch is the minimum match length for LZ4. Matches shorter
// than this are emitted as literals.
const minMatch = 4

// mflLimit is the maximum literal/match length that can be encoded
// in a single token byte nibble. When the length is >= mflLimit,
// additional bytes are emitted (each byte adds its value, 255
// triggers continuation).
const mflLimit = 15

// hashLog controls the hash table size: 1 << hashLog entries.
// Larger = fewer collisions but more memory. 12 (4096 entries) is
// a reasonable balance for WAL data.
const hashLog = 12

const hashSize = 1 << hashLog

// hashMask is used to map a 32-bit hash to a table index.
const hashMask = hashSize - 1

// CompressBound returns the maximum compressed size for an input of
// length n. Per the LZ4 spec: worst case is the input size plus
// a small overhead per 16 bytes.
func CompressBound(n int) int {
	return n + n/255 + 16
}

// Compress compresses src and returns the compressed bytes as a
// valid LZ4 block (no frame header, no checksum).
// The output is always a valid LZ4 block that Decompress can
// round-trip. For inputs where compression doesn't help, the
// output may be slightly larger than the input (the overhead is
// at most 1 byte for the token, plus a few bytes for very long
// inputs).
func Compress(src []byte) []byte {
	if len(src) == 0 {
		return []byte{}
	}
	dst := make([]byte, 0, CompressBound(len(src)))
	return compressBlock(src, dst)
}

// compressBlock performs the LZ4 block compression of src into dst.
// The caller must have pre-allocated dst with CompressBound(len(src)).
func compressBlock(src, dst []byte) []byte {
	srcLen := len(src)
	var literalStart int

	// Hash table: maps 4-byte sequence hash → last-seen position.
	// A value of -1 means "no prior occurrence".
	table := make([]int, hashSize)
	for i := range table {
		table[i] = -1
	}

	i := 0
	for i < srcLen {
		// Try to find a match starting at i.
		matchLen, matchOff := findMatch(src, i, srcLen, table)

		// No match: advance by one byte (the byte at i becomes
		// a literal) and try again.
		if matchLen < minMatch {
			i++
			continue
		}

		// We have a match at (i, matchOff). Emit the literal run
		// from literalStart..i, then the match.
		literalLen := i - literalStart
		dst = emitSequence(dst, src, literalStart, literalLen, matchOff, matchLen)

		// Advance past the match. Update the hash table for
		// intermediate positions (they could be match starts
		// for subsequent sequences).
		i += matchLen
		for j := literalStart + 1; j < i; j++ {
			if j+minMatch <= srcLen {
				h := hash4(src, j)
				table[h] = j
			}
		}
		literalStart = i
	}

	// Emit the trailing literal run (no match terminator).
	if literalStart < srcLen {
		literalLen := srcLen - literalStart
		dst = emitFinalLiterals(dst, src, literalStart, literalLen)
	}

	return dst
}

// emitSequence appends one LZ4 sequence to dst: [token][literals...][offset][match].
// Per the LZ4 spec, the match length stored in the stream is the
// actual match length MINUS minMatch (since every match is at least
// minMatch bytes long, this saves 2 bits per sequence). The
// decompressor adds minMatch back when reading.
func emitSequence(dst, src []byte, literalStart, literalLen, matchOff, matchLen int) []byte {
	token := byte(0)
	if literalLen >= mflLimit {
		token |= mflLimit << 4
	} else {
		token |= byte(literalLen) << 4
	}
	storedMatch := matchLen - minMatch
	if storedMatch >= mflLimit {
		token |= mflLimit
	} else {
		token |= byte(storedMatch)
	}
	dst = append(dst, token)
	// Extra literal length.
	if literalLen >= mflLimit {
		ll := literalLen - mflLimit
		for ll >= 255 {
			dst = append(dst, 255)
			ll -= 255
		}
		dst = append(dst, byte(ll))
	}
	// Literals.
	dst = append(dst, src[literalStart:literalStart+literalLen]...)
	// Match offset (2 bytes, little-endian).
	dst = append(dst, byte(matchOff), byte(matchOff>>8))
	// Extra match length.
	if storedMatch >= mflLimit {
		ml := storedMatch - mflLimit
		for ml >= 255 {
			dst = append(dst, 255)
			ml -= 255
		}
		dst = append(dst, byte(ml))
	}
	return dst
}

// emitFinalLiterals appends the trailing literal-only sequence
// (no match, no offset — this terminates the block).
func emitFinalLiterals(dst, src []byte, literalStart, literalLen int) []byte {
	token := byte(0)
	if literalLen >= mflLimit {
		token |= mflLimit << 4
	} else {
		token |= byte(literalLen) << 4
	}
	// Match length nibble is 0 for the final sequence.
	dst = append(dst, token)
	if literalLen >= mflLimit {
		ll := literalLen - mflLimit
		for ll >= 255 {
			dst = append(dst, 255)
			ll -= 255
		}
		dst = append(dst, byte(ll))
	}
	dst = append(dst, src[literalStart:literalStart+literalLen]...)
	return dst
}

// hash4 returns a 12-bit hash of the 4-byte sequence at position i.
// Caller must ensure at least 4 bytes are available at i.
func hash4(src []byte, i int) int {
	v := uint32(src[i]) | uint32(src[i+1])<<8 | uint32(src[i+2])<<16 | uint32(src[i+3])<<24
	return int((v*2654435761)>>(32-hashLog)) & hashMask
}

// findMatch looks for the longest match starting at src[i] in the
// preceding src[0..i-1]. Returns (matchLength, offset) or
// (0, 0) if no match of at least minMatch is found.
// table is updated in place with position i so that future positions
// can match against it. The update happens even when no match is
// found (or when the position is too close to the start to have a
// valid match), so the hash table always reflects the most recent
// occurrence of each 4-byte sequence.
func findMatch(src []byte, i, srcLen int, table []int) (int, int) {
	// Need at least 4 bytes from i for a match and at least 4
	// bytes of history (offset >= 4).
	if i+minMatch > srcLen {
		return 0, 0
	}
	h := hash4(src, i)
	candidate := table[h]
	// Record this position in the hash table.
	table[h] = i
	if i < minMatch {
		return 0, 0
	}
	// Validate candidate: must be in range, within 16-bit offset,
	// and have matching 4 bytes.
	if candidate < 0 {
		return 0, 0
	}
	offset := i - candidate
	if offset < minMatch || offset > 65535 {
		return 0, 0
	}
	if !bytesEqual4(src[candidate:candidate+4], src[i:i+4]) {
		return 0, 0
	}
	// Extend the match forward.
	matchLen := minMatch
	for i+matchLen < srcLen && src[candidate+matchLen] == src[i+matchLen] {
		matchLen++
	}
	return matchLen, offset
}

// bytesEqual4 returns true if a[0:4] == b[0:4]. Both slices must
// have at least 4 bytes.
func bytesEqual4(a, b []byte) bool {
	return a[0] == b[0] && a[1] == b[1] && a[2] == b[2] && a[3] == b[3]
}

// Decompress decompresses src (a valid LZ4 block) and returns the
// decompressed bytes. Returns an error if src is malformed.
// The function does not validate a checksum — the WAL CRC32 on
// the outer record envelope provides integrity.
func Decompress(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	dst := make([]byte, 0, len(src)*2)
	pos := 0
	for pos < len(src) {
		// Read sequence token.
		token := src[pos]
		pos++
		literalLen := int(token >> 4)
		matchLen := int(token & 0x0F)

		// Read extra literal length bytes.
		if literalLen == mflLimit {
			for {
				if pos >= len(src) {
					return nil, fmt.Errorf("%w: literal length truncated", ErrMalformedInput)
				}
				b := src[pos]
				pos++
				literalLen += int(b)
				if b != 255 {
					break
				}
			}
		}
		// Read literal bytes.
		if pos+literalLen > len(src) {
			return nil, fmt.Errorf("%w: literal data truncated pos=%d literalLen=%d srcLen=%d", ErrMalformedInput, pos, literalLen, len(src))
		}
		dst = append(dst, src[pos:pos+literalLen]...)
		pos += literalLen

		// End-of-block: a sequence with no match (no offset, no
		// match length) is the last sequence in the block. We
		// detect this when pos has reached the end of src.
		if pos >= len(src) {
			break
		}
		// Read 2-byte little-endian match offset.
		if pos+2 > len(src) {
			return nil, fmt.Errorf("%w: match offset truncated pos=%d srcLen=%d", ErrMalformedInput, pos, len(src))
		}
		offset := int(src[pos]) | int(src[pos+1])<<8
		pos += 2
		if offset == 0 || offset > len(dst) {
			return nil, fmt.Errorf("%w: match offset=%d dstLen=%d", ErrOffsetOutOfRange, offset, len(dst))
		}
		// Read extra match length bytes.
		if matchLen == mflLimit {
			for {
				if pos >= len(src) {
					return nil, fmt.Errorf("%w: match length truncated", ErrMalformedInput)
				}
				b := src[pos]
				pos++
				matchLen += int(b)
				if b != 255 {
					break
				}
			}
		}
		matchLen += minMatch
		// Copy match: dst[matchStart..] repeated matchLen times.
		// We copy byte-by-byte so that overlapping references
		// (RLE-style, matchLen > offset) resolve correctly: the
		// just-appended byte is immediately visible for the next
		// copy.
		matchStart := len(dst) - offset
		for k := 0; k < matchLen; k++ {
			dst = append(dst, dst[matchStart+k])
		}
	}
	return dst, nil
}
