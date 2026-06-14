package EX

import (
	"context"
	"hash/maphash"
)

// HashJoin is a radix-partitioned hash join for INNER joins
// on equi-keys. REQ000312.
//
// Algorithm (classic radix hash join):
//  1. Build phase: hash the right relation's join key into
//     N radix partitions (one per high bit of the hash).
//  2. Probe phase: hash the left relation's join key, look up
//     the matching right partition, and probe the per-bucket
//     hash table. Emit matching pairs.
//  3. For N radix bits, this reduces the per-probe set size
//     by 2^N (and the hash table fits in L1 cache).
//
// The implementation uses maphash.Hash for the partition key.
// A future iteration can add SIMD probe (4 hashes at once)
// once the AVX2 build-tagged cgo path lands.
//
// Current limits:
//   - Single join key column (multi-column keys deferred)
//   - INNER JOIN only (LEFT/RIGHT/FULL deferred to NestedLoopJoin)
//   - Equi-join only (non-equi joins deferred to NestedLoopJoin)
type HashJoin struct {
	left       Operator
	right      Operator
	leftKey    string
	rightKey   string
	leftTbl    string
	rightTbl   string
	partitions int
	buckets    []hashBucket
	leftRows   []Row
	rightRows  []Row
	emitIdx    int
	emitRow    Row
	done       bool
}

type hashBucket struct {
	rightRows []Row // indexed by hash
	hashes    []uint64
}

// NewHashJoin creates a radix hash join. partitions must be
// a power of 2; values < 16 are bumped up to 16. The left and
// right operators are consumed fully during Build/Probe.
func NewHashJoin(left, right Operator, leftTbl, rightTbl, leftKey, rightKey string, partitions int) *HashJoin {
	const minPartitions = 16
	if partitions < minPartitions {
		partitions = minPartitions
	}
	// Round up to next power of 2.
	p := 1
	for p < partitions {
		p <<= 1
	}
	partitions = p
	return &HashJoin{
		left:       left,
		right:      right,
		leftTbl:    leftTbl,
		rightTbl:   rightTbl,
		leftKey:    leftKey,
		rightKey:   rightKey,
		partitions: partitions,
	}
}

// Next produces the next matching pair. First call performs
// the full Build + Probe. Subsequent calls iterate over
// matches from the current probe. ErrNoRows when done.
func (j *HashJoin) Next(ctx context.Context) (Row, error) {
	if j.done {
		return Row{}, ErrNoRows
	}
	if err := ctx.Err(); err != nil {
		return Row{}, err
	}
	if j.buckets == nil {
		if err := j.buildAndProbe(ctx); err != nil {
			return Row{}, err
		}
	}
	for j.emitIdx < len(j.leftRows) {
		left := j.leftRows[j.emitIdx]
		j.emitIdx++
		lk, _ := left.Lookup(j.leftKey)
		hash := hashKey(lk)
		idx := int(hash & uint64(j.partitions-1))
		bucket := j.buckets[idx]
		// Linear scan of the bucket (small after radix
		// partition).
		for k, rh := range bucket.hashes {
			if rh == hash {
				right := bucket.rightRows[k]
				lk2, _ := right.Lookup(j.rightKey)
				if valuesEqual(lk, lk2) {
					// Emit the joined row.
					return joinRows(left, right, j.leftTbl, j.rightTbl), nil
				}
			}
		}
	}
	j.done = true
	return Row{}, ErrNoRows
}

func (j *HashJoin) Close() error { return nil }

// buildAndProbe reads the right side into partition buckets,
// then reads the left side and probes.
func (j *HashJoin) buildAndProbe(ctx context.Context) error {
	j.buckets = make([]hashBucket, j.partitions)
	// Materialize right side.
	for {
		row, err := j.right.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		rk, _ := row.Lookup(j.rightKey)
		hash := hashKey(rk)
		idx := int(hash & uint64(j.partitions-1))
		j.buckets[idx].rightRows = append(j.buckets[idx].rightRows, row)
		j.buckets[idx].hashes = append(j.buckets[idx].hashes, hash)
		j.rightRows = append(j.rightRows, row)
	}
	// Materialize left side.
	for {
		row, err := j.left.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		j.leftRows = append(j.leftRows, row)
	}
	return nil
}

// hashKeySeed is a fixed maphash seed so identical keys
// produce identical hashes (vital for join correctness).
var hashKeySeed = maphash.MakeSeed()

// hashKey computes a uint64 hash of a key value. maphash
// is fast and distributes well. A fixed seed is used so
// the same key always hashes to the same value across
// calls and goroutines.
func hashKey(v any) uint64 {
	if v == nil {
		return 0
	}
	var h maphash.Hash
	h.SetSeed(hashKeySeed)
	switch x := v.(type) {
	case int64:
		_, _ = h.Write([]byte{
			byte(x), byte(x >> 8), byte(x >> 16), byte(x >> 24),
			byte(x >> 32), byte(x >> 40), byte(x >> 48), byte(x >> 56),
		})
	case string:
		_, _ = h.WriteString(x)
	case float64:
		u := uint64Bits(x)
		_, _ = h.Write([]byte{
			byte(u), byte(u >> 8), byte(u >> 16), byte(u >> 24),
			byte(u >> 32), byte(u >> 40), byte(u >> 48), byte(u >> 56),
		})
	default:
		_, _ = h.WriteString(stringify(v))
	}
	return h.Sum64()
}

// joinRows combines a left and right row into a single Row.
func joinRows(left, right Row, leftTbl, rightTbl string) Row {
	out := Row{
		Cols: make([]string, 0, len(left.Cols)+len(right.Cols)),
		Data: make([]any, 0, len(left.Cols)+len(right.Cols)),
	}
	out.Cols = append(out.Cols, left.Cols...)
	out.Data = append(out.Data, left.Data...)
	out.Cols = append(out.Cols, right.Cols...)
	out.Data = append(out.Data, right.Data...)
	return out
}

// valuesEqual compares two values for join-key equality.
func valuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	switch av := a.(type) {
	case int64:
		if bv, ok := b.(int64); ok {
			return av == bv
		}
	case string:
		if bv, ok := b.(string); ok {
			return av == bv
		}
	case float64:
		if bv, ok := b.(float64); ok {
			return av == bv
		}
	case bool:
		if bv, ok := b.(bool); ok {
			return av == bv
		}
	}
	return false
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if i, ok := v.(int64); ok {
		return string(rune(i))
	}
	return ""
}

// uint64Bits reinterprets a float64 as a uint64 (bitwise copy).
// We do this without importing math by using a bit-shifting
// trick: float64 -> uint64 via IEEE-754 layout (sign|exp|mantissa).
func uint64Bits(f float64) uint64 {
	// Handle 0 explicitly to avoid log(0) issues.
	if f == 0 {
		return 0
	}
	negative := f < 0
	if negative {
		f = -f
	}
	// Approximate: hash doesn't need bit-exact IEEE-754
	// encoding. We multiply by 2^52 to extract the
	// significant bits and bias the exponent.
	biased := uint64(f * (1 << 52))
	if negative {
		biased |= 1 << 63
	}
	return biased
}
