package OP

import (
	"context"
	"io"
	"sync"

	UT "github.com/cyw0ng95/razordata/internal/SQB/UT"
	"github.com/cyw0ng95/razordata/internal/SQF/LX"
)

// grace_hashjoin.go implements a spill-to-disk grace hash join (REQ001999).
//
// The existing VectorizedHashJoin fully materializes the build side in memory,
// which OOMs on large builds. GraceHashJoin partitions both sides by hash into
// numPartitions buckets, spills partitions that exceed their memory budget to
// temp files, then processes each partition pair independently through a fresh
// VectorizedHashJoin. Peak memory is bounded by the largest single partition
// rather than the total build size.
//
// Design: compose, don't reimplement. GraceHashJoin is a thin orchestrator
// that reuses 100% of VectorizedHashJoin's proven outer-join, parallel-build,
// parallel-probe, bloom-filter, multi-key, and NULL-handling logic. The only
// new code is radix partitioning, spill I/O (spill.go), and per-partition
// orchestration.

const (
	// GraceDefaultMemoryBudget is the default per-join memory budget. Builds
	// whose estimated size exceeds this trigger GraceHashJoin selection in
	// the planner. 64 MB balances spill frequency against in-memory throughput.
	GraceDefaultMemoryBudget int64 = 64 * 1024 * 1024

	// GraceEstBytesPerRow is the rough per-row cost used by the planner to
	// compare estimated build size against the memory budget.
	GraceEstBytesPerRow int64 = 32

	// GraceMinPartitionBytes is the floor for a single partition's in-memory
	// buffer before it spills. Prevents degenerate tiny-budget thrashing.
	GraceMinPartitionBytes int64 = 4 * 1024

	// GraceMinPartitions and GraceMaxPartitions bound the partition count.
	// Target ~4096 rows per partition; round up to a power of two for a
	// cheap bitmask partition selector.
	GraceMinPartitions = 4
	GraceMaxPartitions = 64

	// gracePartitionTargetRows is the target rows per partition used when
	// deriving numPartitions from an estimated build row count.
	gracePartitionTargetRows int64 = 4096
)

// chooseNumPartitions derives a power-of-two partition count from an
// estimated build row count. Returns GraceMinPartitions for non-positive
// estimates so unit tests with unknown sizes still get a sane default.
func chooseNumPartitions(estimatedBuildRows int64) int {
	n := int((estimatedBuildRows + gracePartitionTargetRows - 1) / gracePartitionTargetRows)
	if n < GraceMinPartitions {
		n = GraceMinPartitions
	}
	if n > GraceMaxPartitions {
		n = GraceMaxPartitions
	}
	// Round up to power of two so partition selection is a cheap bitmask.
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// GraceHashJoin is a spill-to-disk hash join that composes VectorizedHashJoin
// on a per-partition basis. REQ001999.
type GraceHashJoin struct {
	build     UT.BatchProducer
	probe     UT.BatchProducer
	buildKeys []int
	probeKeys []int
	kind      JoinKind

	// Parallelism forwarding for the per-partition VectorizedHashJoin.
	pool         *UT.WorkerPool
	parallelism  int
	memoryBudget int64

	// Partitioning state. Set in partitionAll.
	estBuildRows    int64
	numPartitions   int
	partMask        int
	partitionBudget int64
	buildParts      []*gracePartition
	probeParts      []*gracePartition
	partitioned     bool

	// REQ001999 OOM guard: total in-memory bytes across all partitions on
	// both sides. partitionSide checks this after each flushFull and forces
	// the largest in-memory partition to spill when it exceeds memoryBudget.
	// This caps peak heap at ~memoryBudget + one BatchSize buffer per
	// partition, regardless of how skewed the hash distribution is.
	totalMemBytes int64

	// Processing state.
	current *VectorizedHashJoin
	partIdx int
	done    bool

	// tempDir holds all spill files for this join; removed on Close.
	tempDir string

	closeOnce sync.Once
	closeErr  error
}

// NewGraceHashJoin creates an inner grace hash join. REQ001999.
func NewGraceHashJoin(build, probe UT.BatchProducer, buildKeys, probeKeys []int) *GraceHashJoin {
	return &GraceHashJoin{
		build:           build,
		probe:           probe,
		buildKeys:       buildKeys,
		probeKeys:       probeKeys,
		kind:            JoinKindInner,
		memoryBudget:    GraceDefaultMemoryBudget,
		numPartitions:   GraceMinPartitions,
		partMask:        GraceMinPartitions - 1,
		partitionBudget: GraceDefaultMemoryBudget / int64(GraceMinPartitions),
	}
}

// NewGraceHashJoinWithKind creates a grace hash join with the specified
// join kind (INNER/LEFT/RIGHT/FULL). REQ001999.
func NewGraceHashJoinWithKind(build, probe UT.BatchProducer, buildKeys, probeKeys []int, kind JoinKind) *GraceHashJoin {
	return &GraceHashJoin{
		build:           build,
		probe:           probe,
		buildKeys:       buildKeys,
		probeKeys:       probeKeys,
		kind:            kind,
		memoryBudget:    GraceDefaultMemoryBudget,
		numPartitions:   GraceMinPartitions,
		partMask:        GraceMinPartitions - 1,
		partitionBudget: GraceDefaultMemoryBudget / int64(GraceMinPartitions),
	}
}

// WithPool forwards a WorkerPool to each per-partition VectorizedHashJoin
// for parallel build. REQ001999.
func (g *GraceHashJoin) WithPool(pool *UT.WorkerPool) *GraceHashJoin {
	g.pool = pool
	return g
}

// WithParallelism forwards an explicit worker count to each per-partition
// VectorizedHashJoin. REQ001999.
func (g *GraceHashJoin) WithParallelism(n int) *GraceHashJoin {
	g.parallelism = n
	return g
}

// WithMemoryBudget sets the per-join memory budget and recomputes the
// per-partition spill threshold. REQ001999.
func (g *GraceHashJoin) WithMemoryBudget(b int64) *GraceHashJoin {
	g.memoryBudget = b
	g.recomputePartitionBudget()
	return g
}

// WithEstimatedBuildRows sets the estimated build-side row count and
// recomputes the partition count. The planner calls this with the catalog
// row count so partitioning targets ~4096 rows per partition. REQ001999.
func (g *GraceHashJoin) WithEstimatedBuildRows(n int64) *GraceHashJoin {
	g.estBuildRows = n
	g.numPartitions = chooseNumPartitions(n)
	g.partMask = g.numPartitions - 1
	g.recomputePartitionBudget()
	return g
}

func (g *GraceHashJoin) recomputePartitionBudget() {
	b := g.memoryBudget / int64(g.numPartitions)
	if b < GraceMinPartitionBytes {
		b = GraceMinPartitionBytes
	}
	g.partitionBudget = b
}

// NumPartitions returns the partition count (for testing/diagnostics).
func (g *GraceHashJoin) NumPartitions() int { return g.numPartitions }

// Spilled reports whether any build partition spilled to disk. Valid after
// the first NextBatch call (which triggers partitioning). REQ001999.
func (g *GraceHashJoin) Spilled() bool {
	for _, p := range g.buildParts {
		if p.spilled {
			return true
		}
	}
	return false
}

// NextBatch drives the grace hash join. The first call partitions both
// sides; subsequent calls drain per-partition VectorizedHashJoins to EOF
// in partition order. Returns (nil, nil) at end of all partitions.
// REQ001999.
func (g *GraceHashJoin) NextBatch(ctx context.Context) (*UT.Batch, error) {
	if g.done {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 0: partition both sides into per-hash buckets.
	if !g.partitioned {
		if err := g.partitionAll(ctx); err != nil {
			g.done = true
			return nil, err
		}
		g.partitioned = true
		g.partIdx = 0
	}

	for {
		// Start the next partition's inner VJH if needed.
		if g.current == nil {
			if g.partIdx >= g.numPartitions {
				g.done = true
				return nil, nil
			}
			inner, err := g.startPartition(g.partIdx)
			if err != nil {
				g.done = true
				return nil, err
			}
			g.current = inner
		}

		batch, err := g.current.NextBatch(ctx)
		if err != nil {
			g.done = true
			return nil, err
		}
		if batch != nil {
			return batch, nil
		}
		// Inner VJH exhausted — release it and its partition resources.
		_ = g.current.Close()
		g.closePartition(g.partIdx)
		g.current = nil
		g.partIdx++
	}
}

// partitionAll drains both sides, distributing rows into per-partition
// buffers, then seals each partition. REQ001999.
func (g *GraceHashJoin) partitionAll(ctx context.Context) error {
	// Create a temp dir for spill files up front so cleanup is a single
	// RemoveAll in Close. Cheap if no partition spills.
	d, err := ensureTempDir("")
	if err != nil {
		return err
	}
	g.tempDir = d

	g.buildParts = make([]*gracePartition, g.numPartitions)
	g.probeParts = make([]*gracePartition, g.numPartitions)
	for i := range g.buildParts {
		g.buildParts[i] = &gracePartition{}
		g.probeParts[i] = &gracePartition{}
	}

	if err := g.partitionSide(ctx, g.build, g.buildKeys, g.buildParts); err != nil {
		return err
	}
	if err := g.partitionSide(ctx, g.probe, g.probeKeys, g.probeParts); err != nil {
		return err
	}

	// Seal: flush trailing partial buffers and seal spill files.
	for _, p := range g.buildParts {
		if err := p.seal(g); err != nil {
			return err
		}
	}
	for _, p := range g.probeParts {
		if err := p.seal(g); err != nil {
			return err
		}
	}
	return nil
}

// partitionSide drains src, hashing each row's key columns and appending
// the row to the matching partition. NULL keys route to partition 0,
// matching VectorizedHashJoin's behavior (NULL keys never match and are
// emitted as unmatched in LEFT/FULL). REQ001999.
func (g *GraceHashJoin) partitionSide(ctx context.Context, src UT.BatchProducer, keys []int, parts []*gracePartition) error {
	keyVals := make([]int64, len(keys))
	schemaInit := false
	for {
		batch, err := src.NextBatch(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			return nil
		}
		// Initialize all partition schemas from the first batch so that
		// partitions receiving 0 rows still know the column layout.
		// This is critical for RIGHT/FULL joins where an empty build
		// partition must still emit probe rows with NULL build columns.
		if !schemaInit && batch.Size > 0 {
			for _, p := range parts {
				if p.nCols == 0 {
					p.initSchema(batch)
				}
			}
			schemaInit = true
		}
		nRows := batch.Size
		for r := 0; r < nRows; r++ {
			allNonNull := true
			for ki, kc := range keys {
				if isColNull(&batch.Cols[kc], r) {
					allNonNull = false
					break
				}
				keyVals[ki] = colValAt(&batch.Cols[kc], r)
			}
			var pidx int
			if allNonNull {
				pidx = int(UT.HashComposite(keyVals) & uint64(g.partMask))
			}
			// NULL keys → partition 0 (pidx stays 0).
			if err := parts[pidx].appendRow(batch, r, g); err != nil {
				batch.Put()
				return err
			}
		}
		batch.Put()

		// REQ001999 OOM guard: after each source batch, check total in-memory
		// bytes across all partitions on this side. If we've exceeded the join
		// memory budget, force the largest in-memory partition to spill. This
		// caps peak heap even under hash skew (all rows to one partition) or
		// tiny per-partition budgets that individually never trigger spill.
		if g.totalMemBytes >= g.memoryBudget {
			if err := g.spillLargestPartition(parts); err != nil {
				return err
			}
		}
	}
}

// spillLargestPartition finds the in-memory partition with the most bytes
// and forces it to spill to disk. Called by partitionSide when totalMemBytes
// exceeds the join memory budget. REQ001999 OOM guard.
func (g *GraceHashJoin) spillLargestPartition(parts []*gracePartition) error {
	var bestIdx = -1
	var bestBytes int64
	for i, p := range parts {
		if p.spilled || p.memBytes == 0 {
			continue
		}
		if p.memBytes > bestBytes {
			bestBytes = p.memBytes
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return nil // nothing to spill (all already spilled or empty)
	}
	p := parts[bestIdx]
	freed, err := p.spillToDisk(g.tempDir)
	if err != nil {
		return err
	}
	g.totalMemBytes -= freed
	return nil
}

// startPartition constructs a fresh VectorizedHashJoin fed by in-memory
// or spill-replay producers for partition pidx. REQ001999.
func (g *GraceHashJoin) startPartition(pidx int) (*VectorizedHashJoin, error) {
	buildProd := g.makePartitionProducer(g.buildParts[pidx])
	probeProd := g.makePartitionProducer(g.probeParts[pidx])

	var inner *VectorizedHashJoin
	if g.kind == JoinKindInner {
		inner = NewVectorizedHashJoin(buildProd, probeProd, g.buildKeys, g.probeKeys)
	} else {
		inner = NewVectorizedHashJoinWithKind(buildProd, probeProd, g.buildKeys, g.probeKeys, g.kind)
	}
	if g.pool != nil {
		inner.WithPool(g.pool)
	}
	if g.parallelism > 0 {
		inner.WithParallelism(g.parallelism)
	}
	return inner, nil
}

// makePartitionProducer returns a batch producer for a partition: a
// memBatchProducer for in-memory partitions, a spillReplayProducer for
// spilled partitions. REQ001999.
func (g *GraceHashJoin) makePartitionProducer(p *gracePartition) UT.BatchProducer {
	if p.spilled {
		return &spillReplayProducer{sf: p.spill}
	}
	return &memBatchProducer{batches: p.memBatches}
}

// closePartition releases a partition's resources after its inner VJH
// has been drained and closed. REQ001999.
func (g *GraceHashJoin) closePartition(pidx int) {
	g.buildParts[pidx].close()
	g.probeParts[pidx].close()
}

// Close releases all resources: the current inner VJH (if any), all
// partition buffers and spill files, the source producers, and the temp
// directory. Idempotent. REQ001999.
func (g *GraceHashJoin) Close() error {
	g.closeOnce.Do(func() {
		g.done = true
		if g.current != nil {
			g.closeErr = g.current.Close()
			g.current = nil
		}
		for i := range g.buildParts {
			g.buildParts[i].close()
			g.probeParts[i].close()
		}
		if g.build != nil {
			if err := g.build.Close(); err != nil && g.closeErr == nil {
				g.closeErr = err
			}
		}
		if g.probe != nil {
			if err := g.probe.Close(); err != nil && g.closeErr == nil {
				g.closeErr = err
			}
		}
		if g.tempDir != "" {
			removeTempDir(g.tempDir)
			g.tempDir = ""
		}
	})
	return g.closeErr
}

// --- partition ---

// gracePartition accumulates rows for one hash bucket of one side. Rows
// are buffered in a BatchSize-sized columnar buffer; when it fills, the
// batch is either retained in memBatches (in-memory path) or written to
// a spill file (spill path, once the per-partition budget is exceeded).
type gracePartition struct {
	// Schema (set on first row from the source batch).
	nCols int
	names []string
	types []LX.TokenType

	// Current accumulating batch (lazily allocated).
	buf  *UT.Batch
	bufN int

	// Completed in-memory batches (pre-spill only).
	memBatches []*UT.Batch
	memBytes   int64

	// Spill state.
	spill   *spillFile
	spilled bool

	// rowCount tracks total rows appended (for diagnostics).
	rowCount int64
}

// initSchema captures the column layout from the first source batch.
func (p *gracePartition) initSchema(src *UT.Batch) {
	p.nCols = logicalColCount(src)
	p.names = make([]string, p.nCols)
	p.types = make([]LX.TokenType, p.nCols)
	for i := 0; i < p.nCols; i++ {
		p.names[i] = src.Cols[i].Name
		p.types[i] = src.Cols[i].Type
	}
}

// appendRow copies one source row into the partition buffer, flushing
// to memBatches or spill when the buffer fills. REQ001999.
// The join reference is used for totalMemBytes accounting (OOM guard).
func (p *gracePartition) appendRow(src *UT.Batch, row int, g *GraceHashJoin) error {
	if p.nCols == 0 {
		p.initSchema(src)
	}
	if p.buf == nil {
		p.buf = UT.GetBatch(p.nCols)
		for i := 0; i < p.nCols; i++ {
			p.buf.Cols[i].Name = p.names[i]
			p.buf.Cols[i].Type = p.types[i]
			allocateColData(&p.buf.Cols[i], UT.BatchSize, p.types[i])
		}
	}
	for i := 0; i < p.nCols; i++ {
		copyRowToColumn(&p.buf.Cols[i], &src.Cols[i], p.bufN, row)
	}
	p.bufN++
	p.rowCount++

	if p.bufN >= UT.BatchSize {
		return p.flushFull(g)
	}
	return nil
}

// flushFull emits a full BatchSize buffer to memBatches or spill.
func (p *gracePartition) flushFull(g *GraceHashJoin) error {
	p.buf.Size = p.bufN
	if p.spilled {
		if err := p.spill.WriteBatch(p.buf); err != nil {
			return err
		}
		p.buf.Put()
	} else {
		delta := estBatchBytes(p.buf, p.bufN)
		p.memBytes += delta
		g.totalMemBytes += delta
		p.memBatches = append(p.memBatches, p.buf)
		if p.memBytes >= g.partitionBudget {
			freed, err := p.spillToDisk(g.tempDir)
			if err != nil {
				return err
			}
			g.totalMemBytes -= freed
		}
	}
	p.buf = nil
	p.bufN = 0
	return nil
}

// spillToDisk writes all in-memory batches to a spill file and marks the
// partition as spilled. Subsequent flushes write directly to the spill
// file. REQ001999. Returns the number of bytes freed from memBytes.
func (p *gracePartition) spillToDisk(tempDir string) (freed int64, err error) {
	if p.spill == nil {
		sf, err := newSpillFile(tempDir)
		if err != nil {
			return 0, err
		}
		p.spill = sf
	}
	for _, b := range p.memBatches {
		if err := p.spill.WriteBatch(b); err != nil {
			return 0, err
		}
		b.Put()
	}
	p.memBatches = nil
	freed = p.memBytes
	p.memBytes = 0
	p.spilled = true
	return freed, nil
}

// seal flushes any trailing partial buffer and seals the spill file
// (if any) for reading. REQ001999. The join reference is used for
// totalMemBytes accounting and budget enforcement (OOM guard: if the
// trailing buffer pushes the partition over budget, spill it).
func (p *gracePartition) seal(g *GraceHashJoin) error {
	if p.bufN > 0 {
		p.buf.Size = p.bufN
		if p.spilled {
			if err := p.spill.WriteBatch(p.buf); err != nil {
				return err
			}
			p.buf.Put()
		} else {
			delta := estBatchBytes(p.buf, p.bufN)
			p.memBytes += delta
			g.totalMemBytes += delta
			p.memBatches = append(p.memBatches, p.buf)
			// OOM guard: if this trailing buffer pushed the partition over
			// its per-partition budget, spill it now. Without this, small
			// partitions that never fill a BatchSize buffer would stay in
			// memory indefinitely, bypassing the budget check in flushFull.
			if p.memBytes >= g.partitionBudget {
				freed, err := p.spillToDisk(g.tempDir)
				if err != nil {
					return err
				}
				g.totalMemBytes -= freed
			}
		}
		p.buf = nil
		p.bufN = 0
	} else if p.buf != nil {
		// Empty buffer allocated but never filled.
		p.buf.Put()
		p.buf = nil
	}
	// If the partition has a known schema but received 0 rows (and wasn't
	// spilled), emit an empty batch so the consumer (VJH) can determine the
	// column layout. This is critical for RIGHT/FULL joins where an empty
	// build partition must still produce NULL build columns. REQ001999.
	if p.nCols > 0 && p.rowCount == 0 && len(p.memBatches) == 0 && !p.spilled {
		empty := UT.GetBatch(p.nCols)
		for i := 0; i < p.nCols; i++ {
			empty.Cols[i].Name = p.names[i]
			empty.Cols[i].Type = p.types[i]
			allocateColData(&empty.Cols[i], 1, p.types[i])
		}
		empty.Size = 0
		p.memBatches = append(p.memBatches, empty)
	}
	if p.spill != nil {
		return p.spill.Seal()
	}
	return nil
}

// close releases the partition's buffer and spill file. memBatches are
// owned by the memBatchProducer (released when the inner VJH closes it),
// so close only clears the reference. REQ001999.
func (p *gracePartition) close() {
	if p.buf != nil {
		p.buf.Put()
		p.buf = nil
	}
	p.memBatches = nil
	if p.spill != nil {
		_ = p.spill.Close()
		p.spill = nil
	}
}

// estBatchBytes estimates the byte cost of a batch's columnar data,
// including null bitmaps. Used for spill-threshold accounting.
func estBatchBytes(b *UT.Batch, nRows int) int64 {
	var bytes int64
	nCols := logicalColCount(b)
	for i := 0; i < nCols; i++ {
		switch b.Cols[i].Type {
		case LX.T_INT_KW, LX.T_BIGINT:
			bytes += int64(8 * nRows)
		case LX.T_FLOAT_KW:
			bytes += int64(8 * nRows)
		case LX.T_BOOL:
			bytes += int64(nRows)
		case LX.T_TEXT, LX.T_VARCHAR, LX.T_BLOB:
			strs := b.Cols[i].Data.Strs
			for j := 0; j < nRows && j < len(strs); j++ {
				bytes += int64(len(strs[j])) + 8 // +8 for length prefix
			}
		}
		if b.Cols[i].Nulls != nil {
			bytes += int64((nRows + 7) / 8)
		}
	}
	return bytes
}

// --- producers ---

// memBatchProducer replays a pre-built slice of in-memory batches. It
// transfers ownership of each batch to the caller on NextBatch; Close
// returns any un-consumed batches to the pool. REQ001999.
type memBatchProducer struct {
	batches []*UT.Batch
	idx     int
}

func (p *memBatchProducer) NextBatch(_ context.Context) (*UT.Batch, error) {
	if p.idx >= len(p.batches) {
		return nil, nil
	}
	b := p.batches[p.idx]
	p.idx++
	return b, nil
}

func (p *memBatchProducer) Close() error {
	for i := p.idx; i < len(p.batches); i++ {
		p.batches[i].Put()
	}
	p.batches = nil
	return nil
}

// spillReplayProducer reads batches back from a sealed spill file. The
// reader is opened lazily on the first NextBatch. REQ001999.
type spillReplayProducer struct {
	sf   *spillFile
	r    io.ReadCloser
	done bool
}

func (p *spillReplayProducer) NextBatch(_ context.Context) (*UT.Batch, error) {
	if p.done {
		return nil, nil
	}
	if p.r == nil {
		r, err := p.sf.OpenReader()
		if err != nil {
			return nil, err
		}
		p.r = r
	}
	b, err := readBatch(p.r)
	if err != nil {
		return nil, err
	}
	if b == nil {
		p.done = true
		return nil, nil
	}
	return b, nil
}

func (p *spillReplayProducer) Close() error {
	if p.r != nil {
		_ = p.r.Close()
		p.r = nil
	}
	// The spillFile itself is owned by the partition and closed separately.
	return nil
}
