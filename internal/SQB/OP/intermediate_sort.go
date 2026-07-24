package OP

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	ec "github.com/cyw0ng95/razordata/internal/LOG/EC"
	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	LX "github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

type Sort struct {
	child        Operator
	keys         []PS.OrderItem
	buf          []Row
	pos          int
	materialized bool
	params       []any
	pool         *UT.WorkerPool // REQ001050: parallel sort support
	sortBufferSize int64
	// REQ001278: when true, data is already in sort order (e.g. ORDER BY
	// primary key on SeqScan output). Skip materialization + sort.
	preOrdered bool
	// REQ001332: collRegistry resolves collation names to user-defined
	// comparison functions. nil = no custom collations registered.
	collRegistry func(string) DT.CollateFunc

	closed atomic.Bool
}

// Child returns the sort's child operator.
func (s *Sort) Child() Operator      { return s.child }
func (s *Sort) SetChild(c Operator)  { s.child = c }
func (s *Sort) Keys() []PS.OrderItem { return s.keys }

func NewSort(child Operator, keys []PS.OrderItem) *Sort {
	return &Sort{child: child, keys: keys}
}

// SetPreOrdered marks this sort as unnecessary — the child already
// produces rows in the correct order. REQ001278.
func (s *Sort) SetPreOrdered() *Sort {
	s.preOrdered = true
	return s
}

// WithPool attaches a WorkerPool for parallel sort. REQ001050.
func (s *Sort) WithPool(pool *UT.WorkerPool) *Sort {
	s.pool = pool
	return s
}

// WithSortBufferSize caps the in-memory materialization for sorting.
// 0 = unlimited. REQ001065.
func (s *Sort) WithSortBufferSize(v int64) *Sort {
	s.sortBufferSize = v
	return s
}

// WithCollationRegistry sets the collation name→function lookup. REQ001332.
func (s *Sort) WithCollationRegistry(fn func(string) DT.CollateFunc) *Sort {
	s.collRegistry = fn
	return s
}

// WithParams propagates the bound `?` placeholders (R16-1..2).
func (s *Sort) WithParams(p []any) Operator {
	s.params = p
	return s
}

func (s *Sort) Next(ctx context.Context) (Row, error) {
	ec.BUG_ON(s.closed.Load(), "Sort.Next() after Close()")

	// REQ001278: pre-ordered fast path — rows are already in correct order.
	if s.preOrdered {
		return s.child.Next(ctx)
	}

	if !s.materialized {
		// REQ001987: if child implements BatchProducer (and for SeqScan,
		// only when it has a store — otherwise NextBatch errors out),
		// drain via NextBatch + ToRows for lower per-row overhead.
		useBatch := false
		if bp, ok := s.child.(UT.BatchProducer); ok {
			if ss, isSeq := s.child.(*SeqScan); isSeq {
				useBatch = ss.Store() != nil
			} else {
				useBatch = true
			}
			if useBatch {
				for {
					batch, err := bp.NextBatch(ctx)
					if err != nil {
						return Row{}, err
					}
					if batch == nil {
						break
					}
					rows := batch.ToRows()
					s.buf = append(s.buf, rows...)
					if batch.Pooled {
						batch.Put()
					}
				}
			}
		}
		if !useBatch {
			for {
				row, err := s.child.Next(ctx)
				if err != nil {
					if err == ErrNoRows {
						break
					}
					return Row{}, err
				}
				s.buf = append(s.buf, row)
			}
		}

		// REQ000768+REQ000773: pre-extract sort keys into a parallel
		// keyCache slice, then sort an index array in-place.
		// Eliminates the sortRow allocation and double-buffering.
		// REQ001025: use a flat buffer to avoid N allocations.
		// REQ001202+: pre-analyze sort key expressions for direct
		// SlotIdx access, bypassing EvalValue call + type switch +
		// bounds check + EqualFold in the hot inner loop.
		n := len(s.buf)
		numKeys := len(s.keys)
		keyAccess := make([]struct {
			isSlot  bool
			slotIdx int
		}, numKeys)
		if n > 0 {
			for j, k := range s.keys {
				switch e := k.Expr.(type) {
				case *PS.Ident:
					if e.SlotIdx >= 0 && e.SlotIdx < len(s.buf[0].Data) {
						keyAccess[j] = struct {
							isSlot  bool
							slotIdx int
						}{isSlot: true, slotIdx: e.SlotIdx}
					}
				case *PS.QualifiedName:
					if e.SlotIdx >= 0 && e.SlotIdx < len(s.buf[0].Data) {
						keyAccess[j] = struct {
							isSlot  bool
							slotIdx int
						}{isSlot: true, slotIdx: e.SlotIdx}
					}
				}
			}
		}
		// REQ001232: detect int-only sort keys for direct int64 compare
		allIntKeys := false
		if n > 0 && numKeys > 0 {
			allIntKeys = true
			for j := range s.keys {
				if !keyAccess[j].isSlot {
					allIntKeys = false
					break
				}
				idx := keyAccess[j].slotIdx
				if idx >= 0 && idx < len(s.buf[0].Types) {
					t := s.buf[0].Types[idx]
					if t == LX.T_INT_KW || t == LX.T_BIGINT {
						continue
					}
				}
				allIntKeys = false
				break
			}
		}

		if allIntKeys {
			// Int-only path: extract int64 keys directly, compare with <
			flatIntKeys := make([]int64, n*numKeys)
			for i, r := range s.buf {
				base := i * numKeys
				for j := range s.keys {
					flatIntKeys[base+j] = r.Data[keyAccess[j].slotIdx].I64
				}
			}
			// REQ001634: for N ≤ 64, use stack-allocated index array
			// to avoid heap allocation.
			var indices []int
			var smallBuf [64]int
			if n <= 64 {
				indices = smallBuf[:n]
			} else {
				indices = make([]int, n)
			}
			for i := range indices {
				indices[i] = i
			}
			slices.SortStableFunc(indices, func(ai, bi int) int {
				ka, kb := flatIntKeys[ai*numKeys:(ai+1)*numKeys], flatIntKeys[bi*numKeys:(bi+1)*numKeys]
				for ki := range ka {
					if ka[ki] < kb[ki] {
						if s.keys[ki].Desc {
							return 1
						}
						return -1
					}
					if ka[ki] > kb[ki] {
						if s.keys[ki].Desc {
							return -1
						}
						return 1
					}
				}
				return 0
			})
			// REQ001634: apply permutation in-place by following cycles.
			// Eliminates the reordered allocation and full buffer copy.
			applyPermutation(s.buf, indices)
		} else {
			keyCache := make([][]Value, n)
			flatKeys := make([]Value, n*numKeys)
			for i, r := range s.buf {
				sk := flatKeys[i*numKeys : (i+1)*numKeys]
				var err error
				for j, k := range s.keys {
					var v Value
					if keyAccess[j].isSlot {
						v = r.Data[keyAccess[j].slotIdx]
					} else {
						v, err = EV.EvalValue(k.Expr, &r, s.params)
						if err != nil {
							return Row{}, err
						}
					}
					sk[j] = v
				}
				keyCache[i] = sk
			}

			// REQ001050: parallel sort when pool is available and > threshold
			if s.pool != nil && n > 10000 {
				if err := s.parallelSort(ctx, keyCache); err != nil {
					return Row{}, err
				}
			} else {
				var indices []int
				var smallBuf [64]int
				if n <= 64 {
					indices = smallBuf[:n]
				} else {
					indices = make([]int, n)
				}
				for i := range indices {
					indices[i] = i
				}
				slices.SortStableFunc(indices, func(ai, bi int) int {
					ka, kb := keyCache[ai], keyCache[bi]
					for ki := range ka {
						if s.keys[ki].NullsOrder != 0 {
							if ka[ki].IsNull() && !kb[ki].IsNull() {
								return -int(s.keys[ki].NullsOrder)
							}
							if kb[ki].IsNull() && !ka[ki].IsNull() {
								return int(s.keys[ki].NullsOrder)
							}
						}
						var c int
						if s.keys[ki].Collation != "" && s.collRegistry != nil {
							if coll := s.collRegistry(s.keys[ki].Collation); coll != nil {
								c = DT.CompareValueWithCollation(ka[ki], kb[ki], coll)
							} else {
								c = pl.CompareValue(ka[ki], kb[ki])
							}
						} else {
							c = pl.CompareValue(ka[ki], kb[ki])
						}
						if c == 0 {
							continue
						}
						if s.keys[ki].Desc {
							return -c
						}
						return c
					}
					return 0
				})

				applyPermutation(s.buf, indices)
			}
		}
		s.materialized = true
	}
	if s.pos >= len(s.buf) {
		return Row{}, ErrNoRows
	}
	r := s.buf[s.pos]
	s.pos++
	return r, nil
}

// applyPermutation reorders buf in-place according to indices.
// After the call, buf[i] is the element that was at indices[i].
// Uses cycle-following — O(n) time, O(1) extra space.
// REQ001634: eliminates the reordered allocation and full buffer copy.
func applyPermutation[T any](buf []T, indices []int) {
	n := len(indices)
	for i := 0; i < n; i++ {
		if indices[i] == i {
			continue
		}
		// Start a cycle at i.
		curr := i
		saved := buf[i]
		for {
			next := indices[curr]
			if next == i {
				buf[curr] = saved
				indices[curr] = curr
				break
			}
			buf[curr] = buf[next]
			indices[curr] = curr
			curr = next
		}
	}
}

// ParallelSortThreshold is the minimum row count for parallel sort.
const ParallelSortThreshold = 10000

func (s *Sort) parallelSort(ctx context.Context, keyCache [][]Value) error {
	n := len(s.buf)
	workers := s.pool.Workers()
	if workers < 2 {
		workers = 2
	}

	// Sample-based partition: pick workers-1 splitters from keyCache
	sampleStep := n / (workers * 4)
	if sampleStep < 1 {
		sampleStep = 1
	}
	samples := make([]int, 0, workers*4)
	for i := 0; i < n && len(samples) < workers*4; i += sampleStep {
		samples = append(samples, i)
	}
	slices.SortStableFunc(samples, func(a, b int) int {
		ka, kb := keyCache[a], keyCache[b]
		for ki := range ka {
			c := DT.CompareValue(ka[ki], kb[ki])
			if c == 0 {
				continue
			}
			return c
		}
		return 0
	})

	// Pick every 4th sample as a splitter
	splitters := make([][]Value, 0, workers-1)
	for i := 4; i < len(samples) && len(splitters) < workers-1; i += 4 {
		splitters = append(splitters, keyCache[samples[i]])
	}
	if len(splitters) == 0 {
		// Single partition — sort sequentially
		indices := make([]int, n)
		for i := range indices {
			indices[i] = i
		}
		slices.SortStableFunc(indices, func(ai, bi int) int {
			return s.cmpKeys(keyCache[ai], keyCache[bi])
		})
		reordered := make([]Row, n)
		for i, idx := range indices {
			reordered[i] = s.buf[idx]
		}
		s.buf = reordered
		return nil
	}

	// Partition rows by splitters
	partitions := make([][]int, len(splitters)+1)
	for i := range partitions {
		partitions[i] = make([]int, 0, n/(len(splitters)+1))
	}
	for i := 0; i < n; i++ {
		key := keyCache[i]
		placed := false
		for pi, split := range splitters {
			if s.cmpKeys(key, split) < 0 {
				partitions[pi] = append(partitions[pi], i)
				placed = true
				break
			}
		}
		if !placed {
			partitions[len(partitions)-1] = append(partitions[len(partitions)-1], i)
		}
	}

	// Sort each partition in parallel
	type partResult struct {
		idx  int
		rows []Row
		err  error
	}
	resultCh := make(chan partResult, len(partitions))
	var wg sync.WaitGroup

	for pi, part := range partitions {
		if len(part) == 0 {
			continue
		}
		pi2, part2 := pi, part
		wg.Add(1)
		err := s.pool.Submit(ctx, func() error {
			defer wg.Done()
			slices.SortStableFunc(part2, func(a, b int) int {
				return s.cmpKeys(keyCache[a], keyCache[b])
			})
			out := make([]Row, len(part2))
			for j, idx := range part2 {
				out[j] = s.buf[idx]
			}
			resultCh <- partResult{idx: pi2, rows: out}
			return nil
		})
		if err != nil {
			wg.Done()
			resultCh <- partResult{idx: pi2, err: err}
			break
		}
	}

	wg.Wait()
	close(resultCh)

	ordered := make([][]Row, len(partitions))
	for res := range resultCh {
		if res.err != nil {
			return res.err
		}
		ordered[res.idx] = res.rows
	}
	var all []Row
	for _, part := range ordered {
		all = append(all, part...)
	}
	s.buf = all
	return nil
}

// cmpKeys compares two sort key Value slices, respecting DESC/NullsOrder.
func (s *Sort) cmpKeys(a, b []Value) int {
	for ki := range a {
		if s.keys[ki].NullsOrder != 0 {
			if a[ki].IsNull() && !b[ki].IsNull() {
				return -int(s.keys[ki].NullsOrder)
			}
			if b[ki].IsNull() && !a[ki].IsNull() {
				return int(s.keys[ki].NullsOrder)
			}
		}
		c := DT.CompareValue(a[ki], b[ki])
		if c == 0 {
			continue
		}
		if s.keys[ki].Desc {
			return -c
		}
		return c
	}
	return 0
}

func (s *Sort) Close() error {
	s.closed.Store(true)
	s.buf = nil
	s.pos = 0
	s.materialized = false
	return s.child.Close()
}

// Reset reinitializes Sort cursor. Does NOT close the child. REQ001464.
func (s *Sort) Reset(ctx context.Context) error { s.buf = nil; s.pos = 0; s.materialized = false; return nil }
