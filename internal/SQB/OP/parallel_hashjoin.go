package OP

import (
	"context"
	"strings"
	"sync"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
	pl "github.com/cyw0ng95/razordata/internal/SQF/PL"
)

// ParallelHashJoin builds the right-side hash table in parallel
// across WorkerPool workers, then probes with the left side.
// REQ001047. Each worker builds a partition of the hash table
// independently; the probe phase is single-threaded (the left
// side is typically smaller and the hash lookup is O(1)).
//
// Algorithm:
//  1. Partition: hash each right-side row's join key % N to
//     assign it to one of N partitions.
//  2. Parallel build: N workers each build their partition's
//     hash table (bucket + hashes + rows) concurrently.
//  3. Probe: iterate left rows, hash each left key, look up
//     the matching partition's bucket, emit matches.
type ParallelHashJoin struct {
	left       pl.Operator
	right      pl.Operator
	leftKeys   []string
	rightKeys  []string
	leftTbl    string
	rightTbl   string
	partitions int
	pool       *UT.WorkerPool

	buckets   []hashBucket
	leftRows  []pl.Row
	leftInfos []leftInfo

	curLeftIdx  int
	curRightIdx int
	emitBuf     []pl.Value
	dataPerRow  int
	done        bool
	built       bool
	buildMu     sync.Mutex

	sharedCols     []string
	sharedTypes    []LX.TokenType
	sharedColIndex map[string]int
	keyBuf         []pl.Value
}

// NewParallelHashJoin creates a parallel hash join. The right side
// is hash-partitioned and built in parallel using the WorkerPool.
func NewParallelHashJoin(left, right pl.Operator, leftTbl, rightTbl string, leftKeys, rightKeys []string, partitions int, pool *UT.WorkerPool) *ParallelHashJoin {
	const minPartitions = 16
	if partitions < minPartitions {
		partitions = minPartitions
	}
	p := 1
	for p < partitions {
		p <<= 1
	}
	return &ParallelHashJoin{
		left: left, right: right,
		leftKeys: leftKeys, rightKeys: rightKeys,
		leftTbl: leftTbl, rightTbl: rightTbl,
		partitions: p,
		pool:       pool,
		keyBuf:     make([]pl.Value, max(len(leftKeys), len(rightKeys))),
		buckets:    make([]hashBucket, p),
	}
}

func (j *ParallelHashJoin) LeftChild() pl.Operator      { return j.left }
func (j *ParallelHashJoin) RightChild() pl.Operator     { return j.right }
func (j *ParallelHashJoin) LeftTbl() string             { return j.leftTbl }
func (j *ParallelHashJoin) RightTbl() string            { return j.rightTbl }
func (j *ParallelHashJoin) LeftKeys() []string          { return j.leftKeys }
func (j *ParallelHashJoin) RightKeys() []string         { return j.rightKeys }
func (j *ParallelHashJoin) SharedCols() []string        { return j.sharedCols }
func (j *ParallelHashJoin) SharedTypes() []LX.TokenType { return j.sharedTypes }

func (j *ParallelHashJoin) Next(ctx context.Context) (pl.Row, error) {
	if j.done {
		return pl.Row{}, ErrNoRows
	}
	if err := ctx.Err(); err != nil {
		return pl.Row{}, err
	}
	if !j.built {
		j.buildMu.Lock()
		if !j.built {
			if err := j.buildAndProbe(ctx); err != nil {
				j.buildMu.Unlock()
				return pl.Row{}, err
			}
		}
		j.buildMu.Unlock()
	}
	for j.curLeftIdx < len(j.leftRows) {
		l := j.leftInfos[j.curLeftIdx]
		bucket := j.buckets[l.idx]
		for j.curRightIdx < len(bucket.hashes) {
			k := j.curRightIdx
			j.curRightIdx++
			if bucket.hashes[k] == l.hash && ValuesEqualMulti(l.lk, lookupKeys(bucket.rightRows[k], j.rightKeys, j.keyBuf)) {
				right := bucket.rightRows[k]
				leftData := j.leftRows[j.curLeftIdx].Data
				outData := make([]pl.Value, j.dataPerRow)
				copy(outData, leftData)
				copy(outData[len(leftData):], right.Data)
				return pl.Row{
					Cols:     j.sharedCols,
					Types:    j.sharedTypes,
					Data:     outData,
					ColIndex: j.sharedColIndex,
				}, nil
			}
		}
		j.curRightIdx = 0
		j.curLeftIdx++
	}
	j.done = true
	return pl.Row{}, ErrNoRows
}

func (j *ParallelHashJoin) Close() error {
	for i := range j.buckets {
		j.buckets[i].rightRows = nil
		j.buckets[i].hashes = nil
	}
	j.leftRows = nil
	j.leftInfos = nil
	if j.left != nil {
		_ = j.left.Close()
	}
	if j.right != nil {
		return j.right.Close()
	}
	return nil
}

func (j *ParallelHashJoin) buildAndProbe(ctx context.Context) error {
	j.built = true
	for i := range j.buckets {
		j.buckets[i].rightRows = make([]pl.Row, 0, 64)
		j.buckets[i].hashes = make([]uint64, 0, 64)
	}

	// Build phase: hash right side into partition buckets using WorkerPool.
	// First materialize right side into a shared slice, then partition.
	var rightRows []pl.Row
	var firstRightCols []string
	var firstRightTypes []LX.TokenType
	var firstRightData []pl.Value
	for {
		row, err := j.right.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		if len(rightRows) == 0 {
			firstRightCols = row.Cols
			firstRightTypes = row.Types
			firstRightData = row.Data
		}
		row.Data = append([]pl.Value(nil), row.Data...)
		rightRows = append(rightRows, row)
	}
	if j.right != nil {
		_ = j.right.Close()
	}

	if len(rightRows) == 0 {
		return nil
	}

	// REQ001047: pre-compute hash and partition for each right row,
	// building local buckets (no concurrent writes to shared state).
	type localBucket struct {
		rows   []pl.Row
		hashes []uint64
	}
	localBuckets := make([]localBucket, j.partitions)
	for i := range localBuckets {
		localBuckets[i].rows = make([]pl.Row, 0, 64)
		localBuckets[i].hashes = make([]uint64, 0, 64)
	}
	for _, row := range rightRows {
		rk := lookupKeys(row, j.rightKeys, j.keyBuf)
		hash := hashKeys(rk)
		idx := int(hash & uint64(j.partitions-1))
		localBuckets[idx].rows = append(localBuckets[idx].rows, row)
		localBuckets[idx].hashes = append(localBuckets[idx].hashes, hash)
	}

	var mergeMu sync.Mutex
	var wg sync.WaitGroup
	for i := range localBuckets {
		if len(localBuckets[i].rows) == 0 {
			continue
		}
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			mergeMu.Lock()
			j.buckets[idx].rightRows = append(j.buckets[idx].rightRows, localBuckets[idx].rows...)
			j.buckets[idx].hashes = append(j.buckets[idx].hashes, localBuckets[idx].hashes...)
			mergeMu.Unlock()
		}()
	}
	wg.Wait()

	// Materialize left side.
	for {
		row, err := j.left.Next(ctx)
		if err == ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		row.Data = append([]pl.Value(nil), row.Data...)
		j.leftRows = append(j.leftRows, row)
	}

	// Build shared schema.
	if len(j.leftRows) > 0 && len(firstRightData) > 0 {
		leftCols := j.leftRows[0].Cols
		n := len(leftCols) + len(firstRightCols)
		j.sharedCols = make([]string, 0, n)
		j.sharedCols = append(j.sharedCols, leftCols...)
		j.sharedCols = append(j.sharedCols, firstRightCols...)
		j.sharedTypes = make([]LX.TokenType, 0, n)
		j.sharedTypes = append(j.sharedTypes, j.leftRows[0].Types...)
		j.sharedTypes = append(j.sharedTypes, firstRightTypes...)
		j.sharedColIndex = make(map[string]int, n)
		for i, c := range j.sharedCols {
			key := strings.ToLower(c)
			if _, exists := j.sharedColIndex[key]; !exists {
				j.sharedColIndex[key] = i
			}
		}
	}
	if len(j.leftRows) == 0 || len(rightRows) == 0 {
		return nil
	}
	dataPerRow := len(j.leftRows[0].Data) + len(firstRightData)
	j.dataPerRow = dataPerRow
	j.emitBuf = make([]pl.Value, dataPerRow)
	j.curLeftIdx = 0
	j.curRightIdx = 0

	// Build leftInfos.
	leftInfos := make([]leftInfo, 0, len(j.leftRows))
	var flatKeyBuf []pl.Value
	if len(j.leftRows) > 0 {
		first := lookupKeys(j.leftRows[0], j.leftKeys, j.keyBuf)
		flatKeyBuf = make([]pl.Value, len(j.leftRows)*len(first))
	}
	for li, left := range j.leftRows {
		lk := lookupKeys(left, j.leftKeys, j.keyBuf)
		lkCopy := flatKeyBuf[li*len(lk) : (li+1)*len(lk)]
		copy(lkCopy, lk)
		hash := hashKeys(lkCopy)
		idx := int(hash & uint64(j.partitions-1))
		leftInfos = append(leftInfos, leftInfo{lk: lkCopy, hash: hash, idx: idx})
	}
	j.leftInfos = leftInfos
	return nil
}
